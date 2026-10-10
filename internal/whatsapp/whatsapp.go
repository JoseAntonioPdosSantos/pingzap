// Package whatsapp keeps the WhatsApp session as a linked device and reports
// when a message that should trigger the alert arrives or when I reply to
// whoever triggered it.
package whatsapp

import (
	"context"
	"fmt"
	"log"
	"os"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"pingzap/internal/config"
)

// Handlers receive the number involved in the event, as it was configured.
type Handlers struct {
	// OnTrigger is called for each incoming message that passes the number
	// and keyword filters.
	OnTrigger func(number string)
	// OnReply is called when I send, from any device, a message to one of
	// the configured numbers.
	OnReply func(number string)
}

// Connect opens the session and starts calling the handlers. On the first
// run it shows the pairing QR code.
func Connect(ctx context.Context, cfg config.WhatsApp, h Handlers) (*whatsmeow.Client, error) {
	container, err := sqlstore.New(ctx, "sqlite3", cfg.DB, waLog.Noop)
	if err != nil {
		return nil, fmt.Errorf("open the WhatsApp session: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("load the WhatsApp device: %w", err)
	}

	wa := whatsmeow.NewClient(device, waLog.Noop)
	users := triggerUsers(cfg.Numbers)

	wa.AddEventHandler(func(evt any) {
		switch e := evt.(type) {
		case *events.Connected:
			log.Println("connected to WhatsApp")
		case *events.LoggedOut:
			log.Println("WhatsApp session logged out; delete whatsapp.db and pair again")
		case *events.Message:
			if e.Info.IsGroup {
				return
			}
			if e.Info.IsFromMe {
				// For my own messages, the chat is the recipient.
				if number, ok := configuredNumber(ctx, wa, users, e.Info.Chat, e.Info.RecipientAlt); ok {
					log.Printf("message sent to %s", number)
					h.OnReply(number)
				}
				return
			}
			number, ok := configuredNumber(ctx, wa, users, e.Info.Sender, e.Info.SenderAlt)
			if !ok || !matchesKeyword(messageText(e), cfg.Keyword) {
				return
			}
			log.Printf("message received from %s", number)
			h.OnTrigger(number)
		}
	})

	if wa.Store.ID != nil {
		if err := wa.Connect(); err != nil {
			return nil, fmt.Errorf("connect to WhatsApp: %w", err)
		}
		return wa, nil
	}

	// First run: pair as a linked device via QR code.
	qrChan, err := wa.GetQRChannel(ctx)
	if err != nil {
		return nil, fmt.Errorf("get the QR code: %w", err)
	}
	if err := wa.Connect(); err != nil {
		return nil, fmt.Errorf("connect to WhatsApp: %w", err)
	}
	go func() {
		for item := range qrChan {
			switch item.Event {
			case "code":
				fmt.Println("Open WhatsApp > Linked devices > Link a device and scan the QR code:")
				qrterminal.GenerateHalfBlock(item.Code, qrterminal.L, os.Stdout)
			case "success":
				log.Println("WhatsApp paired")
			default:
				log.Printf("WhatsApp pairing: %s", item.Event)
			}
		}
	}()
	return wa, nil
}

// configuredNumber returns the configured number the addresses belong to, if
// any. The contact may arrive as a LID (hidden identifier) instead of the
// phone number, so the alternative address and the LID -> phone mapping are
// checked as well.
func configuredNumber(ctx context.Context, wa *whatsmeow.Client, users map[string]string, jids ...types.JID) (string, bool) {
	for _, jid := range jids {
		if jid.IsEmpty() {
			continue
		}
		if jid.Server == types.HiddenUserServer {
			pn, err := wa.Store.LIDs.GetPNForLID(ctx, jid.ToNonAD())
			if err != nil || pn.IsEmpty() {
				continue
			}
			jid = pn
		}
		if jid.Server != types.DefaultUserServer {
			continue
		}
		if number, ok := users[jid.User]; ok {
			return number, true
		}
	}
	return "", false
}

// messageText returns the message text or the media caption.
func messageText(e *events.Message) string {
	m := e.Message
	for _, text := range []string{
		m.GetConversation(),
		m.GetExtendedTextMessage().GetText(),
		m.GetImageMessage().GetCaption(),
		m.GetVideoMessage().GetCaption(),
		m.GetDocumentMessage().GetCaption(),
	} {
		if text != "" {
			return text
		}
	}
	return ""
}
