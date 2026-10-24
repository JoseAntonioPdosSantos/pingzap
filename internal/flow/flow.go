// Package flow runs a chatbot conversation as a state machine: the bot has
// states (e.g. gate closed / open), each state shows a menu, and each menu
// option may publish a command, ask for a confirmation and move the bot to
// another state.
//
// The package is pure: Handle and Remind take the current situation and
// return what to reply, publish and store, so the caller owns all I/O.
package flow

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Flow struct {
	// StartCommand and MenuCommand both show the menu of the current state,
	// e.g. "/start" and "/menu".
	StartCommand string `json:"start_command"`
	MenuCommand  string `json:"menu_command"`
	// ReminderMinutes repeats an unanswered confirmation question; 0 disables.
	ReminderMinutes int `json:"reminder_minutes"`
	// MaxAttempts limits how many times a command is sent when the answer is
	// "no"; 0 means no limit.
	MaxAttempts int `json:"max_attempts"`
	// AlarmAfterReminders escalates an unanswered confirmation: after this
	// many reminders AlarmCommand is published (e.g. "ON": LED and buzzer) so
	// someone checks what is going on. The first answer in the chat then
	// publishes AlarmClearCommand. 0 disables the alarm.
	AlarmAfterReminders int     `json:"alarm_after_reminders"`
	AlarmCommand        string  `json:"alarm_command"`
	AlarmClearCommand   string  `json:"alarm_clear_command"`
	States              []State `json:"states"`
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
	// new state (e.g. "the gate was closed with the remote").
	Command string `json:"command"`
	// Confirm is asked after publishing, answered with 1 (yes) or 2 (no). On
	// "no" the command is published again. Empty skips the confirmation.
	Confirm string `json:"confirm"`
	// NextState is the state the bot moves to on success; empty keeps it.
	NextState string `json:"next_state"`
	// Reply is sent on success; empty sends a default message.
	Reply string `json:"reply"`
}

// Conversation is the progress of one chat through the flow.
type Conversation struct {
	Step Step `json:"step"`
	// StateID and Option identify the menu option being handled.
	StateID string `json:"state_id,omitempty"`
	Option  int    `json:"option,omitempty"`
	// Attempts counts how many times the option's command was published.
	Attempts   int       `json:"attempts,omitempty"`
	PromptedAt time.Time `json:"prompted_at,omitzero"`
	// Reminders counts the reminders sent for the current question; Alarm
	// tells whether they escalated to the alarm.
	Reminders int  `json:"reminders,omitempty"`
	Alarm     bool `json:"alarm,omitempty"`
}

type Step string

const (
	StepIdle    Step = ""
	StepMenu    Step = "menu"
	StepConfirm Step = "confirm"
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
		StartCommand:    "/start",
		MenuCommand:     "/menu",
		ReminderMinutes:     3,
		MaxAttempts:         3,
		AlarmAfterReminders: 3,
		AlarmCommand:        "ON",
		AlarmClearCommand:   "OFF",
		States: []State{
			{
				ID:   "fechado",
				Name: "Fechado",
				Menu: "O portão está *fechado*.",
				Options: []Option{
					{Label: "Abrir o portão", Shortcut: "/abrir", Command: "PULSE", Confirm: "O portão abriu?", NextState: "aberto", Reply: "Portão aberto. Envie /menu quando quiser fechá-lo."},
					{Label: "Informar que o portão foi aberto pelo controle", NextState: "aberto", Reply: "Ok, registrei o portão como aberto."},
				},
			},
			{
				ID:   "aberto",
				Name: "Aberto",
				Menu: "O portão está *aberto*.",
				Options: []Option{
					{Label: "Fechar o portão", Shortcut: "/fechar", Command: "PULSE", Confirm: "O portão fechou?", NextState: "fechado", Reply: "Portão fechado."},
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
	if f.ReminderMinutes < 0 || f.MaxAttempts < 0 || f.AlarmAfterReminders < 0 {
		return errors.New("lembrete, tentativas e alarme não podem ser negativos")
	}
	if f.AlarmAfterReminders > 0 {
		if f.ReminderMinutes == 0 {
			return errors.New("o alarme depende dos lembretes: defina o intervalo de insistência")
		}
		if strings.TrimSpace(f.AlarmCommand) == "" {
			return errors.New("informe o comando do alarme")
		}
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
			if o.Confirm != "" && o.Command == "" {
				return fmt.Errorf("a opção %q tem confirmação mas não envia comando", o.Label)
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
// reports false when the message is not meant for the bot (no conversation
// going on and not a menu command), so the chat stays quiet.
func (f Flow) Handle(current string, c Conversation, text string, now time.Time) (Result, bool) {
	r, ok := f.handle(current, c, text, now)
	// Someone is in the chat again: the alarm for the unanswered question
	// has done its job.
	if ok && c.Step == StepConfirm && c.Alarm {
		r.Conversation.Alarm = false
		r.Conversation.Reminders = 0
		if f.AlarmClearCommand != "" {
			r.Publish = append([]string{f.AlarmClearCommand}, r.Publish...)
		}
		r.Replies = append([]string{"Alarme desligado."}, r.Replies...)
	}
	return r, ok
}

func (f Flow) handle(current string, c Conversation, text string, now time.Time) (Result, bool) {
	text = strings.TrimSpace(text)
	state := f.State(current)

	if f.isMenuCommand(text) {
		return f.showMenu(state), true
	}
	if r, ok := f.shortcut(state, text, now); ok {
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
		return f.choose(state, n-1, now), true

	case StepConfirm:
		opt, ok := f.option(c)
		if !ok {
			// The flow was edited mid-conversation; start over.
			return f.showMenu(state), true
		}
		switch text {
		case "1":
			return f.succeed(opt), true
		case "2":
			if f.MaxAttempts > 0 && c.Attempts >= f.MaxAttempts {
				return Result{Replies: []string{fmt.Sprintf(
					"Não deu certo após %d tentativa(s). Envie %s para tentar de novo.", c.Attempts, f.menuHint())}}, true
			}
			c.Attempts++
			c.PromptedAt = now
			c.Reminders = 0
			return Result{
				Replies:      []string{fmt.Sprintf("Tentando novamente (tentativa %d)...\n\n%s", c.Attempts, confirmPrompt(opt))},
				Publish:      []string{opt.Command},
				Conversation: c,
			}, true
		default:
			c.PromptedAt = now
			return Result{
				Replies:      []string{"Responda 1 para Sim ou 2 para Não.\n\n" + confirmPrompt(opt)},
				Conversation: c,
			}, true
		}
	}
	return Result{Conversation: c}, false
}

// Remind repeats the confirmation question when it has gone unanswered for
// ReminderMinutes, and fires the alarm after AlarmAfterReminders reminders.
// It reports false when there is nothing to send.
func (f Flow) Remind(c Conversation, now time.Time) (Result, bool) {
	if c.Step != StepConfirm || f.ReminderMinutes <= 0 {
		return Result{}, false
	}
	if now.Sub(c.PromptedAt) < time.Duration(f.ReminderMinutes)*time.Minute {
		return Result{}, false
	}
	opt, ok := f.option(c)
	if !ok {
		return Result{}, false
	}
	c.PromptedAt = now
	c.Reminders++
	r := Result{
		Replies:      []string{"Ainda preciso da sua resposta:\n\n" + confirmPrompt(opt)},
		Conversation: c,
	}
	if f.AlarmAfterReminders > 0 && c.Reminders >= f.AlarmAfterReminders && !c.Alarm {
		r.Conversation.Alarm = true
		r.Publish = []string{f.AlarmCommand}
		r.Replies = append(r.Replies, fmt.Sprintf(
			"Sem resposta após %d lembretes: acionei o alarme do dispositivo para alguém verificar o que está acontecendo.", c.Reminders))
	}
	return r, true
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
func (f Flow) shortcut(state State, text string, now time.Time) (Result, bool) {
	if !strings.HasPrefix(text, "/") {
		return Result{}, false
	}
	for i, o := range state.Options {
		if o.Shortcut != "" && strings.EqualFold(o.Shortcut, text) {
			return f.choose(state, i, now), true
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

func (f Flow) choose(s State, index int, now time.Time) Result {
	opt := s.Options[index]
	if opt.Confirm == "" {
		r := f.succeed(opt)
		if opt.Command != "" {
			r.Publish = []string{opt.Command}
		}
		return r
	}
	return Result{
		Replies: []string{"Comando enviado.\n\n" + confirmPrompt(opt)},
		Publish: []string{opt.Command},
		Conversation: Conversation{
			Step:       StepConfirm,
			StateID:    s.ID,
			Option:     index,
			Attempts:   1,
			PromptedAt: now,
		},
	}
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

// option returns the option a confirmation refers to, if it still exists.
func (f Flow) option(c Conversation) (Option, bool) {
	for _, s := range f.States {
		if s.ID == c.StateID && c.Option >= 0 && c.Option < len(s.Options) {
			opt := s.Options[c.Option]
			return opt, opt.Confirm != ""
		}
	}
	return Option{}, false
}

func confirmPrompt(o Option) string {
	return o.Confirm + "\n*1* - Sim\n*2* - Não"
}
