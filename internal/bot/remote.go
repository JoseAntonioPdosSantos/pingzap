package bot

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"

	"pingzap/internal/flow"
	"pingzap/internal/store"
	"pingzap/internal/whatsapp"
)

// The web remote is a page with the commands of a flow bot as buttons. Each
// person gets a personal link from the bot, in a private chat; whatever is
// done through it is recorded under their name and told to the chat where
// they asked for it. A link asked for in a group only works while its owner
// is in that group.

const (
	// retryWindow is how long after a command the remote offers to send it
	// again.
	retryWindow = 2 * time.Minute
	// membersFresh is how long the list of who is in a group is trusted
	// before asking WhatsApp again, so someone who left loses the remote
	// within this time.
	membersFresh = time.Minute
	// membersStale is how long the last list still counts while WhatsApp
	// can not be asked (e.g. it is reconnecting). Past that nobody is
	// confirmed, and the remotes of the group stop until it answers.
	membersStale   = 15 * time.Minute
	membersTimeout = 10 * time.Second
)

// groupMembers is who was in a group when WhatsApp was last asked.
type groupMembers struct {
	phones map[string]bool
	at     time.Time
}

var (
	// ErrRemoteGone means the link does not exist, was revoked, or its bot
	// or chat no longer offers the web remote.
	ErrRemoteGone = errors.New("link inválido ou revogado")
	// ErrRemoteUnverified means WhatsApp could not say whether the person is
	// still in the group, so the remote waits.
	ErrRemoteUnverified = errors.New("não consegui confirmar que você está no grupo; tente novamente em instantes")
	// ErrRemoteStale means the bot is not in the state the page showed.
	ErrRemoteStale = errors.New("o estado mudou; confira antes de tentar de novo")
	// ErrRemoteDevice means the command could not be published.
	ErrRemoteDevice = errors.New("não consegui enviar o comando ao dispositivo; tente novamente em instantes")
)

// Remote is what a web remote shows.
type Remote struct {
	Owner string      `json:"owner"`
	Bot   string      `json:"bot"`
	State RemoteState `json:"state"`
	// Offline is set while the bot's device is not answering.
	Offline bool `json:"offline"`
	// Buttons are the options that send a command, from every state; only
	// those of the current state are enabled.
	Buttons []RemoteButton `json:"buttons"`
	// Fixes are the options of the current state that only correct it (e.g.
	// "the gate was opened with the physical remote").
	Fixes []RemoteButton `json:"fixes"`
	// Retry is the command just sent, while it may be sent again.
	Retry *RemoteButton `json:"retry,omitempty"`
}

type RemoteState struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RemoteButton is the option at index Option of the state State.
type RemoteButton struct {
	State   string `json:"state"`
	Option  int    `json:"option"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
}

// remoteSent is the last command a bot got from a web remote.
type remoteSent struct {
	state    string // state the command was sent from
	option   int
	attempts int
	at       time.Time
}

// sendRemoteLink sends the person who asked their link, in a private chat,
// and says so in the chat when the request came from elsewhere (a group).
func (s *Service) sendRemoteLink(r *route, m whatsapp.Message) {
	if s.publicURL == "" {
		s.send(m.Chat, "O controle remoto web ainda não foi configurado pelo administrador.")
		return
	}
	if m.Sender == "" {
		s.send(m.Chat, "Não consegui identificar o seu número para enviar o controle remoto.")
		return
	}
	name := m.SenderName
	if name == "" {
		name = "+" + m.Sender
	}
	link, err := s.store.EnsureRemoteLink(r.bot.ID, m.Sender, name, chatKey(m), m.Chat.ToNonAD().String())
	if err != nil {
		log.Printf("bot %q: web remote link for %s: %v", r.bot.Name, m.Sender, err)
		s.send(m.Chat, "Não consegui gerar o seu controle remoto. Tente novamente em instantes.")
		return
	}

	// Whoever writes in a group is in it, even if they joined after the list
	// of members was last fetched.
	delete(s.members, m.Group)

	// A contact writing in their own chat is already in private.
	private := m.Chat
	inPrivate := !m.IsGroup() && !m.FromMe
	if !inPrivate {
		private = types.NewJID(m.Sender, types.DefaultUserServer)
	}
	text := fmt.Sprintf("🎛️ *Controle remoto: %s*\n\nToque no link para usar pelo navegador, sem digitar comandos:\n%s/c/%s\n\n"+
		"O link é *pessoal*: tudo o que for feito por ele fica registrado no seu nome e é avisado no grupo. Não compartilhe.",
		r.bot.Name, s.publicURL, link.Token)
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	if err := s.wa.SendText(ctx, private, text); err != nil {
		log.Printf("bot %q: send web remote link to %s: %v", r.bot.Name, m.Sender, err)
		s.send(m.Chat, "Não consegui enviar o controle remoto na sua conversa privada. Tente novamente em instantes.")
		return
	}
	log.Printf("bot %q: web remote link sent to %s", r.bot.Name, m.Sender)
	if !inPrivate {
		s.send(m.Chat, fmt.Sprintf("🎛️ *%s*, enviei o link do seu controle remoto na nossa conversa privada.", name))
	}
}

// remoteRoute finds the link of a token and the bot it controls. The link
// only works while the bot is enabled, offers the web remote and still
// answers the chat where the link was asked for. A link asked for in a
// group also needs its owner to still be in that group.
func (s *Service) remoteRoute(token string, now time.Time) (store.RemoteLink, *route, error) {
	link, err := s.store.RemoteLinkByToken(token)
	if errors.Is(err, store.ErrNotFound) {
		return link, nil, ErrRemoteGone
	}
	if err != nil {
		return link, nil, err
	}
	r, ok := s.bots[link.BotID]
	if !ok || r.bot.Kind != store.KindFlow || r.flow.RemoteCommand == "" || s.routes[link.ChatKey] != r {
		return link, nil, ErrRemoteGone
	}
	if group, ok := strings.CutPrefix(link.ChatKey, "group:"); ok {
		in, err := s.inGroup(group, link.Phone, now)
		if err != nil {
			log.Printf("bot %q: web remote of %s: members of %s: %v", r.bot.Name, link.Phone, group, err)
			return link, nil, ErrRemoteUnverified
		}
		if !in {
			return link, nil, ErrRemoteGone
		}
	}
	return link, r, nil
}

// inGroup reports whether the number is in the group, asking WhatsApp when
// the cached list is older than membersFresh. When WhatsApp can not answer,
// the last list still counts for membersStale; after that it is an error.
func (s *Service) inGroup(group, number string, now time.Time) (bool, error) {
	cached, ok := s.members[group]
	if ok && now.Sub(cached.at) < membersFresh {
		return cached.phones[number], nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), membersTimeout)
	defer cancel()
	list, err := s.wa.GroupMembers(ctx, group)
	if err != nil {
		if ok && now.Sub(cached.at) < membersStale {
			return cached.phones[number], nil
		}
		return false, err
	}
	phones := make(map[string]bool, len(list))
	for _, p := range list {
		phones[p] = true
	}
	s.members[group] = groupMembers{phones: phones, at: now}
	return phones[number], nil
}

// Remote returns what the web remote of a token shows.
func (s *Service) Remote(token string) (Remote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	link, r, err := s.remoteRoute(token, now)
	if err != nil {
		return Remote{}, err
	}
	return s.remoteView(link, r, now), nil
}

func (s *Service) remoteView(link store.RemoteLink, r *route, now time.Time) Remote {
	state := r.flow.State(r.bot.CurrentState)
	v := Remote{
		Owner:   link.Name,
		Bot:     r.bot.Name,
		State:   RemoteState{ID: state.ID, Name: state.Name},
		Buttons: []RemoteButton{},
		Fixes:   []RemoteButton{},
	}
	if r.device != nil {
		_, v.Offline = s.outages[r.device.ID]
	}
	for _, st := range r.flow.States {
		for i, o := range st.Options {
			b := RemoteButton{State: st.ID, Option: i, Label: o.Label, Enabled: st.ID == state.ID}
			switch {
			case o.Command != "":
				v.Buttons = append(v.Buttons, b)
			case b.Enabled:
				v.Fixes = append(v.Fixes, b)
			}
		}
	}
	if sent, ok := s.remoteSent[r.bot.ID]; ok && now.Sub(sent.at) < retryWindow {
		if o, ok := r.flow.Option(sent.state, sent.option); ok && leadsTo(o, sent.state) == state.ID {
			v.Retry = &RemoteButton{State: sent.state, Option: sent.option, Label: o.Label, Enabled: true}
		}
	}
	return v
}

// leadsTo is the state the bot is in after the option o of the state from.
func leadsTo(o flow.Option, from string) string {
	if o.NextState != "" {
		return o.NextState
	}
	return from
}

// RemoteAct runs an option from the web remote of a token: stateID and
// option are what the person pressed, and retry sends again the command
// just sent, as many times as the person wants. Like the chat, it trusts the command and moves to the next
// state right away. The use is only recorded (log and history); nothing is sent to WhatsApp.
//
// With ErrRemoteStale the returned Remote is valid, so the page can show
// what changed.
func (s *Service) RemoteAct(token, stateID string, option int, retry bool) (Remote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	link, r, err := s.remoteRoute(token, now)
	if err != nil {
		return Remote{}, err
	}
	state := r.flow.State(r.bot.CurrentState)
	opt, ok := r.flow.Option(stateID, option)
	if !ok {
		return s.remoteView(link, r, now), ErrRemoteStale
	}

	attempts := 1
	if retry {
		// Only the command just sent, and only while the bot is still where
		// it left it: sending it again later (a relay pulse, for instance)
		// could undo what someone else did.
		sent, ok := s.remoteSent[r.bot.ID]
		if !ok || sent.state != stateID || sent.option != option || now.Sub(sent.at) >= retryWindow ||
			opt.Command == "" || leadsTo(opt, stateID) != state.ID {
			return s.remoteView(link, r, now), ErrRemoteStale
		}
		attempts = sent.attempts + 1
	} else if stateID != state.ID {
		// Someone else changed the state since the page was drawn.
		return s.remoteView(link, r, now), ErrRemoteStale
	}

	if opt.Command != "" {
		if err := s.publish(r, opt.Command); err != nil {
			log.Printf("bot %q: web remote of %s: %v", r.bot.Name, link.Phone, err)
			return Remote{}, ErrRemoteDevice
		}
		s.remoteSent[r.bot.ID] = remoteSent{state: stateID, option: option, attempts: attempts, at: now}
	} else {
		delete(s.remoteSent, r.bot.ID)
	}
	if next := leadsTo(opt, stateID); next != r.bot.CurrentState {
		if err := s.store.SetBotState(r.bot.ID, next); err != nil {
			return Remote{}, err
		}
		log.Printf("bot %q: state %s -> %s (web remote of %s)", r.bot.Name, r.bot.CurrentState, next, link.Phone)
		r.bot.CurrentState = next
	}
	current := r.flow.State(r.bot.CurrentState)

	action := opt.Label
	if retry {
		action = fmt.Sprintf("%s (tentativa %d)", opt.Label, attempts)
	}
	use := store.RemoteUse{Action: action, Command: opt.Command, State: current.Name, At: now}
	if err := s.store.LogRemoteUse(link, use); err != nil {
		log.Printf("bot %q: record web remote use of %s: %v", r.bot.Name, link.Phone, err)
	}
	return s.remoteView(link, r, now), nil
}
