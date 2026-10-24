// Package whatsapp keeps the WhatsApp session as a linked device: pairing by
// QR code, listing contacts and groups, sending text and reporting incoming
// messages.
package whatsapp

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"

	_ "github.com/mattn/go-sqlite3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

// Message is a text message seen in a chat, sent by someone else or by me
// from any device.
type Message struct {
	// Chat is where to reply. For groups, Group is set to the group JID;
	// for one-to-one chats, Phone is the contact's number (digits with the
	// country code), resolved even when WhatsApp hides it behind a LID.
	Chat   types.JID
	Group  string
	Phone  string
	FromMe bool
	Text   string
}

func (m Message) IsGroup() bool { return m.Group != "" }

type State string

const (
	StateDisconnected State = "disconnected"
	StateConnecting   State = "connecting"
	StatePairing      State = "pairing"
	StateConnected    State = "connected"
)

type Status struct {
	State State `json:"state"`
	// QR is the code to scan while pairing.
	QR string `json:"qr,omitempty"`
	// Phone is the paired account's number.
	Phone string `json:"phone,omitempty"`
}

type Contact struct {
	Phone string `json:"phone"`
	Name  string `json:"name"`
}

type Group struct {
	JID  string `json:"jid"`
	Name string `json:"name"`
}

var ErrNotConnected = errors.New("WhatsApp não está conectado")

type Manager struct {
	container *sqlstore.Container
	onMessage func(Message)

	mu     sync.Mutex
	client *whatsmeow.Client
	status Status
	// cancelQR stops waiting for a QR code scan.
	cancelQR context.CancelFunc
}

// New opens the session store. Call Start to connect.
func New(ctx context.Context, dsn string, onMessage func(Message)) (*Manager, error) {
	container, err := sqlstore.New(ctx, "sqlite3", dsn, waLog.Noop)
	if err != nil {
		return nil, fmt.Errorf("open the WhatsApp session: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("load the WhatsApp device: %w", err)
	}
	m := &Manager{container: container, onMessage: onMessage, status: Status{State: StateDisconnected}}
	m.client = m.newClient(device)
	return m, nil
}

// Start connects when a paired session exists. Without one, pairing waits
// for Pair, called from the web interface.
func (m *Manager) Start() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client.Store.ID == nil {
		log.Println("WhatsApp not paired; pair it from the web interface")
		return nil
	}
	m.status = Status{State: StateConnecting}
	if err := m.client.Connect(); err != nil {
		m.status = Status{State: StateDisconnected}
		return fmt.Errorf("connect to WhatsApp: %w", err)
	}
	return nil
}

// Pair starts pairing a new account; the QR code shows up in Status.
func (m *Manager) Pair() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	switch {
	case m.status.State == StatePairing:
		return nil
	case m.client.Store.ID != nil:
		if m.client.IsConnected() {
			return errors.New("já existe uma conta pareada; desconecte-a antes")
		}
		// Paired but offline: just reconnect.
		m.status = Status{State: StateConnecting}
		return m.client.Connect()
	}

	ctx, cancel := context.WithCancel(context.Background())
	qrChan, err := m.client.GetQRChannel(ctx)
	if err != nil {
		cancel()
		return fmt.Errorf("get the QR code: %w", err)
	}
	if err := m.client.Connect(); err != nil {
		cancel()
		return fmt.Errorf("connect to WhatsApp: %w", err)
	}
	m.cancelQR = cancel
	m.status = Status{State: StatePairing}

	client := m.client
	go func() {
		for item := range qrChan {
			m.mu.Lock()
			// Ignore a pairing that Logout already replaced.
			if client == m.client && m.status.State == StatePairing {
				switch item.Event {
				case "code":
					m.status = Status{State: StatePairing, QR: item.Code}
				case "success":
					log.Println("WhatsApp paired")
					m.status = Status{State: StateConnecting}
				default:
					// Timeout or error: the user can ask for a new code.
					log.Printf("WhatsApp pairing: %s", item.Event)
					client.Disconnect()
					m.status = Status{State: StateDisconnected}
				}
			}
			m.mu.Unlock()
		}
	}()
	return nil
}

// Logout unlinks the account from the phone and prepares a fresh device for
// the next pairing.
func (m *Manager) Logout(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancelQR != nil {
		m.cancelQR()
		m.cancelQR = nil
	}
	if m.client.Store.ID != nil {
		if err := m.client.Logout(ctx); err != nil {
			// Unlink locally even if the server could not be told.
			log.Printf("WhatsApp logout: %v", err)
			if err := m.client.Store.Delete(ctx); err != nil {
				return err
			}
		}
	}
	m.client.Disconnect()
	m.client = m.newClient(m.container.NewDevice())
	m.status = Status{State: StateDisconnected}
	return nil
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	if s.State == StateConnected && m.client.Store.ID != nil {
		s.Phone = m.client.Store.ID.User
	}
	return s
}

func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancelQR != nil {
		m.cancelQR()
	}
	m.client.Disconnect()
}

// connected returns the client if it is ready to be used.
func (m *Manager) connected() (*whatsmeow.Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State != StateConnected || !m.client.IsLoggedIn() {
		return nil, ErrNotConnected
	}
	return m.client, nil
}

func (m *Manager) SendText(ctx context.Context, chat types.JID, text string) error {
	client, err := m.connected()
	if err != nil {
		return err
	}
	_, err = client.SendMessage(ctx, chat, &waE2E.Message{Conversation: proto.String(text)})
	return err
}

// Contacts returns the saved contacts that have a phone number.
func (m *Manager) Contacts(ctx context.Context) ([]Contact, error) {
	client, err := m.connected()
	if err != nil {
		return nil, err
	}
	all, err := client.Store.Contacts.GetAllContacts(ctx)
	if err != nil {
		return nil, err
	}
	contacts := []Contact{}
	for jid, info := range all {
		if jid.Server != types.DefaultUserServer {
			continue
		}
		name := firstNonEmpty(info.FullName, info.FirstName, info.PushName, info.BusinessName)
		if name == "" {
			continue
		}
		contacts = append(contacts, Contact{Phone: jid.User, Name: name})
	}
	slices.SortFunc(contacts, func(a, b Contact) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return contacts, nil
}

func (m *Manager) Groups(ctx context.Context) ([]Group, error) {
	client, err := m.connected()
	if err != nil {
		return nil, err
	}
	joined, err := client.GetJoinedGroups(ctx)
	if err != nil {
		return nil, err
	}
	groups := make([]Group, 0, len(joined))
	for _, g := range joined {
		groups = append(groups, Group{JID: g.JID.String(), Name: g.Name})
	}
	slices.SortFunc(groups, func(a, b Group) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return groups, nil
}

func (m *Manager) newClient(device *store.Device) *whatsmeow.Client {
	client := whatsmeow.NewClient(device, waLog.Noop)
	client.AddEventHandler(func(evt any) { m.handleEvent(client, evt) })
	return client
}

func (m *Manager) handleEvent(client *whatsmeow.Client, evt any) {
	switch e := evt.(type) {
	case *events.Connected:
		log.Println("connected to WhatsApp")
		m.setState(client, StateConnected)
	case *events.Disconnected:
		m.setState(client, StateConnecting) // whatsmeow reconnects by itself
	case *events.LoggedOut:
		log.Println("WhatsApp session logged out; pair again from the web interface")
		go m.Logout(context.Background())
	case *events.Message:
		if msg, ok := m.toMessage(client, e); ok {
			m.onMessage(msg)
		}
	}
}

// setState updates the status unless the event comes from a replaced client.
func (m *Manager) setState(client *whatsmeow.Client, state State) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if client == m.client {
		m.status = Status{State: state}
	}
}

func (m *Manager) toMessage(client *whatsmeow.Client, e *events.Message) (Message, bool) {
	text := messageText(e)
	msg := Message{Chat: e.Info.Chat, FromMe: e.Info.IsFromMe, Text: text}
	if e.Info.IsGroup {
		msg.Group = e.Info.Chat.ToNonAD().String()
		return msg, true
	}
	if e.Info.Chat.Server == types.BroadcastServer {
		return Message{}, false // status updates and broadcast lists
	}
	// In one-to-one chats the other party is the chat for my messages and
	// the sender for theirs; either may come as a LID.
	candidates := []types.JID{e.Info.Chat, e.Info.SenderAlt}
	if e.Info.IsFromMe {
		candidates = []types.JID{e.Info.Chat, e.Info.RecipientAlt}
	}
	ctx := context.Background()
	for _, jid := range candidates {
		if jid.IsEmpty() {
			continue
		}
		if jid.Server == types.HiddenUserServer {
			pn, err := client.Store.LIDs.GetPNForLID(ctx, jid.ToNonAD())
			if err != nil || pn.IsEmpty() {
				continue
			}
			jid = pn
		}
		if jid.Server == types.DefaultUserServer {
			msg.Phone = jid.User
			return msg, true
		}
	}
	return Message{}, false
}

// messageText returns the message text or the media caption.
func messageText(e *events.Message) string {
	m := e.Message
	return firstNonEmpty(
		m.GetConversation(),
		m.GetExtendedTextMessage().GetText(),
		m.GetImageMessage().GetCaption(),
		m.GetVideoMessage().GetCaption(),
		m.GetDocumentMessage().GetCaption(),
	)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
