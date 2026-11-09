// Package flow runs a chatbot conversation as a state machine: the bot has
// states (e.g. gate closed / open), each state shows a menu, and each menu
// option may publish a command and move the bot to another state. The bot
// trusts the commands: it does not ask whether they worked, it only offers to
// send them again.
//
// The package is pure: Handle takes the current situation and returns what
// to reply, publish and store, so the caller owns all I/O.
package flow

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Flow struct {
	// StartCommand and MenuCommand both show the menu of the current state,
	// e.g. "/start" and "/menu".
	StartCommand string `json:"start_command"`
	MenuCommand  string `json:"menu_command"`
	// MaxAttempts limits how many times a command is sent, counting the
	// retries; 0 means no limit.
	MaxAttempts int     `json:"max_attempts"`
	States      []State `json:"states"`
}

type State struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Menu is the text shown above the numbered options.
	Menu    string   `json:"menu"`
	Options []Option `json:"options"`
}

type Option struct {
	Label string `json:"label"`
	// Shortcut runs the option straight away when typed (e.g. "/abrir"),
	// without going through the menu. Empty means menu only.
	Shortcut string `json:"shortcut,omitempty"`
	// Command is the payload published to the device; empty only records the
	// new state (e.g. "the gate was closed with the remote"). The bot trusts
	// the command: it moves to NextState right away and offers to send the
	// command again, in case the device did not act.
	Command string `json:"command"`
	// NextState is the state the bot moves to; empty keeps it.
	NextState string `json:"next_state"`
	// Reply is sent once the option runs; empty sends a default message.
	Reply string `json:"reply"`
	// ShowAfterCommand also offers this option, before "try again", right
	// after the commands that lead to its state (e.g. "close the gate" once
	// the gate was opened). The JSON name predates the change and is kept
	// for the saved bots.
	ShowAfterCommand bool `json:"show_in_confirm,omitempty"`
}

// Conversation is the progress of one chat through the flow.
type Conversation struct {
	Step Step `json:"step"`
	// StateID and Option identify the menu option being handled: the menu's
	// state, or the option whose command was just sent.
	StateID string `json:"state_id,omitempty"`
	Option  int    `json:"option,omitempty"`
	// Attempts counts how many times the option's command was published.
	Attempts int `json:"attempts,omitempty"`
}

type Step string

const (
	StepIdle Step = ""
	StepMenu Step = "menu"
	// StepSent follows a command: the bot offers to try again and the
	// options marked ShowAfterCommand.
	StepSent Step = "sent"
)

// Result is what the caller must do after handling a message.
type Result struct {
	Replies []string
	// Publish lists the commands to send to the device, in order.
	Publish []string
	// NewState is the state the bot moves to, if it changes.
	NewState     string
	Conversation Conversation
}

// GateTemplate is a ready-made flow for a gate driven by a relay pulse.
func GateTemplate() Flow {
	return Flow{
		StartCommand: "/start",
		MenuCommand:  "/menu",
		MaxAttempts:  3,
		States: []State{
			{
				ID:   "fechado",
				Name: "Fechado",
				Menu: "O portão está *fechado*.",
				Options: []Option{
					{Label: "Abrir o portão", Shortcut: "/abrir", Command: "PULSE", NextState: "aberto", Reply: "Comando enviado: portão *aberto*."},
					{Label: "Informar que o portão foi aberto pelo controle", NextState: "aberto", Reply: "Ok, registrei o portão como aberto."},
				},
			},
			{
				ID:   "aberto",
				Name: "Aberto",
				Menu: "O portão está *aberto*.",
				Options: []Option{
					{Label: "Fechar o portão", Shortcut: "/fechar", Command: "PULSE", NextState: "fechado", Reply: "Comando enviado: portão *fechado*.", ShowAfterCommand: true},
					{Label: "Informar que o portão foi fechado pelo controle", NextState: "fechado", Reply: "Ok, registrei o portão como fechado."},
				},
			},
		},
	}
}

// Validate checks that the flow can run: commands set, states with unique
// IDs and options pointing at existing states.
func (f Flow) Validate() error {
	if strings.TrimSpace(f.StartCommand) == "" && strings.TrimSpace(f.MenuCommand) == "" {
		return errors.New("defina ao menos um comando para abrir o menu")
	}
	if f.MaxAttempts < 0 {
		return errors.New("o máximo de tentativas não pode ser negativo")
	}
	if len(f.States) == 0 {
		return errors.New("o fluxo precisa de ao menos um estado")
	}
	ids := make(map[string]bool)
	for _, s := range f.States {
		if strings.TrimSpace(s.ID) == "" || strings.TrimSpace(s.Name) == "" {
			return errors.New("todo estado precisa de identificador e nome")
		}
		if ids[s.ID] {
			return fmt.Errorf("estado %q repetido", s.ID)
		}
		ids[s.ID] = true
	}
	for _, s := range f.States {
		if len(s.Options) == 0 {
			return fmt.Errorf("o estado %q não tem opções", s.Name)
		}
		for i, o := range s.Options {
			if strings.TrimSpace(o.Label) == "" {
				return fmt.Errorf("a opção %d do estado %q não tem texto", i+1, s.Name)
			}
			if o.NextState != "" && !ids[o.NextState] {
				return fmt.Errorf("a opção %q leva a um estado que não existe", o.Label)
			}
			if err := f.validateShortcut(s, i); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateShortcut checks an option's shortcut: a single word starting with
// "/", not clashing with the menu commands or another option of the same
// state. The same shortcut may appear in different states.
func (f Flow) validateShortcut(s State, index int) error {
	o := s.Options[index]
	if o.Shortcut == "" {
		return nil
	}
	if !strings.HasPrefix(o.Shortcut, "/") || len(o.Shortcut) < 2 || strings.ContainsAny(o.Shortcut, " \t\n") {
		return fmt.Errorf("o atalho %q da opção %q precisa começar com / e não ter espaços", o.Shortcut, o.Label)
	}
	if f.isMenuCommand(o.Shortcut) {
		return fmt.Errorf("o atalho %q é igual a um comando de menu", o.Shortcut)
	}
	for j, other := range s.Options {
		if j != index && strings.EqualFold(other.Shortcut, o.Shortcut) {
			return fmt.Errorf("o atalho %q se repete no estado %q", o.Shortcut, s.Name)
		}
	}
	return nil
}

// State returns the state with the given ID, falling back to the first one,
// so a bot whose state was removed from the flow still has a menu.
func (f Flow) State(id string) State {
	for _, s := range f.States {
		if s.ID == id {
			return s
		}
	}
	return f.States[0]
}

// Handle processes a message in a chat. current is the bot's state. It
// reports false when the message is not meant for the bot (no menu going on
// and not a command), so the chat stays quiet.
func (f Flow) Handle(current string, c Conversation, text string) (Result, bool) {
	text = strings.TrimSpace(text)
	state := f.State(current)

	if f.isMenuCommand(text) {
		return f.showMenu(state), true
	}
	if r, ok := f.shortcut(state, text); ok {
		return r, true
	}

	switch c.Step {
	case StepMenu:
		// Another chat may have changed the state since this menu was shown.
		if c.StateID != state.ID {
			r := f.showMenu(state)
			r.Replies[0] = "O estado mudou enquanto você escolhia.\n\n" + r.Replies[0]
			return r, true
		}
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > len(state.Options) {
			r := f.showMenu(state)
			r.Replies[0] = "Opção inválida.\n\n" + r.Replies[0]
			return r, true
		}
		return f.choose(state, n-1), true

	case StepSent:
		return f.afterCommand(state, c, text)
	}
	return Result{Conversation: c}, false
}

// afterCommand handles the answer to the options offered after a command:
// those marked ShowAfterCommand, then "try again".
func (f Flow) afterCommand(state State, c Conversation, text string) (Result, bool) {
	n, err := strconv.Atoi(text)
	if err != nil {
		// Nothing was asked: plain chat after a command is not for the bot.
		return Result{Conversation: c}, false
	}
	opt, ok := f.option(c)
	if !ok {
		// The flow was edited mid-conversation; start over.
		return f.showMenu(state), true
	}
	// Another chat changed the state since the command: sending it again
	// (a relay pulse, for instance) could undo what they did.
	if opt.NextState != "" && opt.NextState != state.ID {
		r := f.showMenu(state)
		r.Replies[0] = "O estado mudou desde o último comando.\n\n" + r.Replies[0]
		return r, true
	}
	next, extras := f.extras(opt)
	switch {
	case n >= 1 && n <= len(extras):
		return f.choose(next, extras[n-1]), true
	case n == len(extras)+1 && f.canRetry(c.Attempts):
		c.Attempts++
		prompt, more := f.afterCommandPrompt(opt, c.Attempts)
		r := Result{
			Replies:      []string{fmt.Sprintf("Comando enviado novamente (tentativa %d).\n\n%s", c.Attempts, prompt)},
			Publish:      []string{opt.Command},
			Conversation: c,
		}
		if !more {
			r.Conversation = Conversation{}
		}
		return r, true
	}
	prompt, _ := f.afterCommandPrompt(opt, c.Attempts)
	return Result{Replies: []string{"Opção inválida.\n\n" + prompt}, Conversation: c}, true
}

func (f Flow) isMenuCommand(text string) bool {
	for _, cmd := range []string{f.StartCommand, f.MenuCommand} {
		if cmd = strings.TrimSpace(cmd); cmd != "" && strings.EqualFold(text, cmd) {
			return true
		}
	}
	return false
}

func (f Flow) menuHint() string {
	if f.MenuCommand != "" {
		return f.MenuCommand
	}
	return f.StartCommand
}

// shortcut runs the option of the current state whose shortcut is text. A
// shortcut that only exists in another state (e.g. /abrir with the gate
// already open) is answered with the current menu instead.
func (f Flow) shortcut(state State, text string) (Result, bool) {
	if !strings.HasPrefix(text, "/") {
		return Result{}, false
	}
	for i, o := range state.Options {
		if o.Shortcut != "" && strings.EqualFold(o.Shortcut, text) {
			return f.choose(state, i), true
		}
	}
	for _, s := range f.States {
		for _, o := range s.Options {
			if o.Shortcut != "" && strings.EqualFold(o.Shortcut, text) {
				r := f.showMenu(state)
				r.Replies[0] = fmt.Sprintf("%s não está disponível agora (estado atual: %s).\n\n%s", text, state.Name, r.Replies[0])
				return r, true
			}
		}
	}
	return Result{}, false
}

func (f Flow) showMenu(s State) Result {
	var b strings.Builder
	if s.Menu != "" {
		b.WriteString(s.Menu)
		b.WriteString("\n\n")
	}
	b.WriteString("Digite o número da opção:")
	for i, o := range s.Options {
		fmt.Fprintf(&b, "\n*%d* - %s", i+1, o.Label)
		if o.Shortcut != "" {
			fmt.Fprintf(&b, " (%s)", o.Shortcut)
		}
	}
	return Result{
		Replies:      []string{b.String()},
		Conversation: Conversation{Step: StepMenu, StateID: s.ID},
	}
}

// choose runs an option. When it sends a command, the bot trusts it: it
// moves to the next state right away and offers to send it again.
func (f Flow) choose(s State, index int) Result {
	opt := s.Options[index]
	r := f.succeed(opt)
	if opt.Command == "" {
		return r
	}
	r.Publish = []string{opt.Command}
	prompt, ok := f.afterCommandPrompt(opt, 1)
	if !ok {
		return r
	}
	r.Replies[0] += "\n\n" + prompt
	r.Conversation = Conversation{Step: StepSent, StateID: s.ID, Option: index, Attempts: 1}
	return r
}

func (f Flow) succeed(opt Option) Result {
	reply := opt.Reply
	if reply == "" {
		reply = "Pronto!"
		if opt.NextState != "" {
			reply = fmt.Sprintf("Pronto! Estado atual: %s.", f.State(opt.NextState).Name)
		}
	}
	return Result{Replies: []string{reply}, NewState: opt.NextState}
}

// option returns the option whose command was sent, if it still exists.
func (f Flow) option(c Conversation) (Option, bool) {
	for _, s := range f.States {
		if s.ID == c.StateID && c.Option >= 0 && c.Option < len(s.Options) {
			opt := s.Options[c.Option]
			return opt, opt.Command != ""
		}
	}
	return Option{}, false
}

// canRetry reports whether the command may be sent again after attempts.
func (f Flow) canRetry(attempts int) bool {
	return f.MaxAttempts == 0 || attempts < f.MaxAttempts
}

// afterCommandPrompt lists the options offered after o's command was sent
// attempts times. It reports false when there is none to offer.
func (f Flow) afterCommandPrompt(o Option, attempts int) (string, bool) {
	next, extras := f.extras(o)
	var b strings.Builder
	b.WriteString("Digite o número da opção:")
	for i, index := range extras {
		e := next.Options[index]
		fmt.Fprintf(&b, "\n*%d* - %s", i+1, e.Label)
		if e.Shortcut != "" {
			fmt.Fprintf(&b, " (%s)", e.Shortcut)
		}
	}
	if f.canRetry(attempts) {
		fmt.Fprintf(&b, "\n*%d* - Tentar novamente", len(extras)+1)
	} else {
		if len(extras) == 0 {
			return fmt.Sprintf("Limite de %d tentativa(s) atingido. Se não funcionou, verifique o dispositivo; pelo %s você corrige o estado.", attempts, f.menuHint()), false
		}
		fmt.Fprintf(&b, "\n(limite de %d tentativa(s) atingido)", attempts)
	}
	return b.String(), true
}

// extras returns the state o leads to and the indexes of its options
// offered right after o's command.
func (f Flow) extras(o Option) (State, []int) {
	if o.NextState == "" {
		return State{}, nil
	}
	next := f.State(o.NextState)
	var extras []int
	for i, candidate := range next.Options {
		if candidate.ShowAfterCommand {
			extras = append(extras, i)
		}
	}
	return next, extras
}
