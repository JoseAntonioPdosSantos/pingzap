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
	"pingzap/internal/phone"
	"pingzap/internal/store"
	"pingzap/internal/whatsapp"
)

// The web remote is a page with the commands of a flow bot as buttons. Each
// person gets a personal link from the bot, in a private chat; whatever is
// done through it is recorded under their name. A link asked for in a group
// only works while its owner is in that group. Besides these personal links,
// the management interface makes temporary accesses (store.RemotePass) for
// people outside the chats, limited by dates, uses and hours.

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

// RemoteLocked means a temporary access exists but does not work right now:
// it expired, ran out of uses, has not started or is outside its hours.
// Reason is meant for the person holding the link. A permanent lock never
// lifts; a temporary one lifts with time.
type RemoteLocked struct {
	Reason    string
	Permanent bool
}

func (e *RemoteLocked) Error() string { return e.Reason }

// remoteAccess is who a web remote token belongs to: a person with a link
// from the bot (link), or a temporary access made in the interface (pass).
type remoteAccess struct {
	name string
	link store.RemoteLink
	pass *store.RemotePass
	// lastChance is a pass that just ran out of uses: for a little while it
	// can still send again the command just sent, in case it did not arrive.
	lastChance bool
}

// RemoteAccess describes the limits of a temporary access to whoever holds it.
type RemoteAccess struct {
	// UsesLeft is -1 when there is no limit.
	UsesLeft  int        `json:"uses_left"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Schedule  string     `json:"schedule,omitempty"`
	Note      string     `json:"note,omitempty"`
}

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
	// Access is set for a temporary access.
	Access *RemoteAccess `json:"access,omitempty"`
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

// ErrNoPublicURL means links can not be built: PINGZAP_PUBLIC_URL is not set.
var ErrNoPublicURL = errors.New("o endereço público do pingzap (PINGZAP_PUBLIC_URL) não está configurado")

// SendPassLink sends the link of a temporary access to a phone number on
// WhatsApp, with what the access allows.
func (s *Service) SendPassLink(pass store.RemotePass, number string) error {
	if s.publicURL == "" {
		return ErrNoPublicURL
	}
	number = phone.Normalize(number)
	if len(number) < 10 {
		return &store.ValidationError{Msg: "número de telefone inválido: use DDD + número"}
	}
	b, err := s.store.GetBot(pass.BotID)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	jid, err := s.wa.ContactJID(ctx, number)
	if err != nil {
		return fmt.Errorf("esse número não foi encontrado no WhatsApp: %w", err)
	}

	loc := pass.Location()
	when := func(t time.Time) string { return t.In(loc).Format("02/01 às 15:04") }
	lines := []string{fmt.Sprintf("🎛️ *Controle remoto: %s*", b.Name), "",
		fmt.Sprintf("Olá, %s! Você recebeu um acesso temporário. Toque no link para usar pelo navegador:", pass.Name),
		s.publicURL + "/c/" + pass.Token, ""}
	if pass.StartsAt != nil {
		lines = append(lines, "• Vale a partir de "+when(*pass.StartsAt))
	}
	if pass.ExpiresAt != nil {
		lines = append(lines, "• Vale até "+when(*pass.ExpiresAt))
	}
	if pass.MaxUses > 0 {
		lines = append(lines, fmt.Sprintf("• Limite de %d usos", pass.MaxUses))
	}
	if len(pass.Schedule.Windows) > 0 {
		lines = append(lines, "• Horários: "+pass.Schedule.Describe())
	}
	lines = append(lines, "", "O link é *pessoal*. Não compartilhe.")
	if err := s.wa.SendText(ctx, jid, strings.Join(lines, "\n")); err != nil {
		return fmt.Errorf("não consegui enviar pelo WhatsApp: %w", err)
	}
	log.Printf("bot %q: temporary access %q sent to %s", b.Name, pass.Name, number)
	return nil
}

// remoteRoute finds who a token belongs to and the bot it controls. A
// person's link only works while the bot is enabled, offers the web remote
// and still answers the chat where the link was asked for; a link asked for
// in a group also needs its owner to still be in that group. A temporary
// access works while the bot is an enabled flow bot and the access is valid
// at now, else the error is a *RemoteLocked.
func (s *Service) remoteRoute(token string, now time.Time) (remoteAccess, *route, error) {
	pass, err := s.store.PassByToken(token)
	if err == nil {
		return s.passRoute(pass, now)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return remoteAccess{}, nil, err
	}

	link, err := s.store.RemoteLinkByToken(token)
	if errors.Is(err, store.ErrNotFound) {
		return remoteAccess{}, nil, ErrRemoteGone
	}
	if err != nil {
		return remoteAccess{}, nil, err
	}
	acc := remoteAccess{name: link.Name, link: link}
	r, ok := s.bots[link.BotID]
	if !ok || r.bot.Kind != store.KindFlow || r.flow.RemoteCommand == "" || s.routes[link.ChatKey] != r {
		return acc, nil, ErrRemoteGone
	}
	if group, ok := strings.CutPrefix(link.ChatKey, "group:"); ok {
		in, err := s.inGroup(group, link.Phone, now)
		if err != nil {
			log.Printf("bot %q: web remote of %s: members of %s: %v", r.bot.Name, link.Phone, group, err)
			return acc, nil, ErrRemoteUnverified
		}
		if !in {
			return acc, nil, ErrRemoteGone
		}
	}
	return acc, r, nil
}

func (s *Service) passRoute(pass store.RemotePass, now time.Time) (remoteAccess, *route, error) {
	acc := remoteAccess{name: pass.Name, pass: &pass}
	r, ok := s.bots[pass.BotID]
	if !ok || r.bot.Kind != store.KindFlow {
		return acc, nil, ErrRemoteGone
	}
	status, reason := pass.Status(now)
	switch {
	case status == store.PassActive:
	case status == store.PassUsedUp && pass.LastUsed != nil && now.Sub(*pass.LastUsed) < retryWindow:
		acc.lastChance = true
	default:
		return acc, nil, &RemoteLocked{Reason: reason, Permanent: status.Permanent()}
	}
	return acc, r, nil
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
	acc, r, err := s.remoteRoute(token, now)
	if err != nil {
		return Remote{}, err
	}
	return s.remoteView(acc, r, now), nil
}

func (s *Service) remoteView(acc remoteAccess, r *route, now time.Time) Remote {
	state := r.flow.State(r.bot.CurrentState)
	v := Remote{
		Owner:   acc.name,
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
			b := RemoteButton{State: st.ID, Option: i, Label: o.Label, Enabled: st.ID == state.ID && !acc.lastChance}
			switch {
			case o.Command != "":
				v.Buttons = append(v.Buttons, b)
			case st.ID == state.ID && !acc.lastChance:
				v.Fixes = append(v.Fixes, b)
			}
		}
	}
	if sent, ok := s.remoteSent[r.bot.ID]; ok && now.Sub(sent.at) < retryWindow {
		if o, ok := r.flow.Option(sent.state, sent.option); ok && leadsTo(o, sent.state) == state.ID {
			v.Retry = &RemoteButton{State: sent.state, Option: sent.option, Label: o.Label, Enabled: true}
		}
	}
	if acc.pass != nil {
		v.Access = passAccess(*acc.pass, acc.lastChance)
	}
	return v
}

// passAccess is what the holder of a temporary access is told about it.
func passAccess(p store.RemotePass, lastChance bool) *RemoteAccess {
	a := &RemoteAccess{UsesLeft: p.UsesLeft(), ExpiresAt: p.ExpiresAt}
	if len(p.Schedule.Windows) > 0 {
		a.Schedule = p.Schedule.Describe()
	}
	if lastChance {
		a.Note = "Os usos deste acesso acabaram."
	}
	return a
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
	acc, r, err := s.remoteRoute(token, now)
	if err != nil {
		return Remote{}, err
	}
	if acc.lastChance && !retry {
		return Remote{}, &RemoteLocked{Reason: "Os usos deste acesso acabaram.", Permanent: true}
	}
	state := r.flow.State(r.bot.CurrentState)
	opt, ok := r.flow.Option(stateID, option)
	if !ok {
		return s.remoteView(acc, r, now), ErrRemoteStale
	}

	attempts := 1
	if retry {
		// Only the command just sent, and only while the bot is still where
		// it left it: sending it again later (a relay pulse, for instance)
		// could undo what someone else did.
		sent, ok := s.remoteSent[r.bot.ID]
		if !ok || sent.state != stateID || sent.option != option || now.Sub(sent.at) >= retryWindow ||
			opt.Command == "" || leadsTo(opt, stateID) != state.ID {
			return s.remoteView(acc, r, now), ErrRemoteStale
		}
		attempts = sent.attempts + 1
	} else if stateID != state.ID {
		// Someone else changed the state since the page was drawn.
		return s.remoteView(acc, r, now), ErrRemoteStale
	}

	if opt.Command != "" {
		// A temporary access pays for each new command with one of its uses;
		// sending it again after it failed to arrive is free.
		claimed := false
		if acc.pass != nil && !retry {
			ok, err := s.store.ClaimPassUse(acc.pass.ID)
			if err != nil {
				return Remote{}, err
			}
			if !ok {
				return Remote{}, &RemoteLocked{Reason: "Os usos deste acesso acabaram.", Permanent: true}
			}
			claimed = true
		}
		if err := s.publish(r, opt.Command); err != nil {
			log.Printf("bot %q: web remote of %s: %v", r.bot.Name, acc.name, err)
			if claimed {
				if err := s.store.ReleasePassUse(acc.pass.ID); err != nil {
					log.Printf("bot %q: give back a use of %q: %v", r.bot.Name, acc.name, err)
				}
			}
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
		log.Printf("bot %q: state %s -> %s (web remote of %s)", r.bot.Name, r.bot.CurrentState, next, acc.name)
		r.bot.CurrentState = next
	}
	current := r.flow.State(r.bot.CurrentState)

	action := opt.Label
	if retry {
		action = fmt.Sprintf("%s (tentativa %d)", opt.Label, attempts)
	}
	use := store.RemoteUse{Action: action, Command: opt.Command, State: current.Name, At: now}
	var logErr error
	if acc.pass != nil {
		logErr = s.store.LogPassUse(*acc.pass, use)
		// Show the uses left after this one.
		if fresh, err := s.store.PassByToken(token); err == nil {
			acc.pass = &fresh
		}
	} else {
		logErr = s.store.LogRemoteUse(acc.link, use)
	}
	if logErr != nil {
		log.Printf("bot %q: record web remote use of %s: %v", r.bot.Name, acc.name, logErr)
	}
	return s.remoteView(acc, r, now), nil
}
