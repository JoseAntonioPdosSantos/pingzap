package flow

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)

func TestGateTemplateIsValid(t *testing.T) {
	if err := GateTemplate().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenGate(t *testing.T) {
	f := GateTemplate()

	// Messages outside a conversation are ignored.
	if _, ok := f.Handle("fechado", Conversation{}, "oi", t0); ok {
		t.Fatal("plain message handled without a conversation")
	}

	r, ok := f.Handle("fechado", Conversation{}, " /START ", t0)
	if !ok || r.Conversation.Step != StepMenu || !strings.Contains(r.Replies[0], "*1* - Abrir o portão") {
		t.Fatalf("/start = %+v, %v", r, ok)
	}

	r, _ = f.Handle("fechado", r.Conversation, "1", t0)
	if pub(r) != "PULSE" || r.Conversation.Step != StepConfirm || r.NewState != "" {
		t.Fatalf("option 1 = %+v", r)
	}

	// "No": the command is sent again and the question repeated.
	r, _ = f.Handle("fechado", r.Conversation, "2", t0)
	if pub(r) != "PULSE" || r.Conversation.Attempts != 2 || r.Conversation.Step != StepConfirm {
		t.Fatalf("answer 2 = %+v", r)
	}

	// Anything other than 1 or 2 repeats the question without publishing.
	r, _ = f.Handle("fechado", r.Conversation, "talvez", t0)
	if pub(r) != "" || r.Conversation.Step != StepConfirm {
		t.Fatalf("answer talvez = %+v", r)
	}

	r, _ = f.Handle("fechado", r.Conversation, "1", t0)
	if r.NewState != "aberto" || r.Conversation.Step != StepIdle || pub(r) != "" {
		t.Fatalf("answer 1 = %+v", r)
	}

	// With the gate open, the menu offers to close it.
	r, _ = f.Handle("aberto", Conversation{}, "/menu", t0)
	if !strings.Contains(r.Replies[0], "*1* - Fechar o portão") {
		t.Fatalf("menu when open = %q", r.Replies[0])
	}
}

func TestReportClosedByRemote(t *testing.T) {
	f := GateTemplate()
	r, _ := f.Handle("aberto", Conversation{}, "/menu", t0)
	r, _ = f.Handle("aberto", r.Conversation, "2", t0)
	if r.NewState != "fechado" || pub(r) != "" || r.Conversation.Step != StepIdle {
		t.Fatalf("report closed = %+v", r)
	}
}

func TestMaxAttempts(t *testing.T) {
	f := GateTemplate()
	f.MaxAttempts = 2
	r, _ := f.Handle("fechado", Conversation{}, "/menu", t0)
	r, _ = f.Handle("fechado", r.Conversation, "1", t0) // attempt 1
	r, _ = f.Handle("fechado", r.Conversation, "2", t0) // attempt 2
	if pub(r) != "PULSE" {
		t.Fatalf("second attempt = %+v", r)
	}
	r, _ = f.Handle("fechado", r.Conversation, "2", t0)
	if pub(r) != "" || r.Conversation.Step != StepIdle || r.NewState != "" {
		t.Fatalf("after max attempts = %+v", r)
	}
}

func TestInvalidOptionAndStaleMenu(t *testing.T) {
	f := GateTemplate()
	r, _ := f.Handle("fechado", Conversation{}, "/menu", t0)

	inv, _ := f.Handle("fechado", r.Conversation, "9", t0)
	if pub(inv) != "" || inv.Conversation.Step != StepMenu || !strings.HasPrefix(inv.Replies[0], "Opção inválida") {
		t.Fatalf("invalid option = %+v", inv)
	}

	// Someone else opened the gate meanwhile: option 1 must not close it.
	stale, _ := f.Handle("aberto", r.Conversation, "1", t0)
	if pub(stale) != "" || stale.Conversation.StateID != "aberto" {
		t.Fatalf("stale menu = %+v", stale)
	}
}

func TestRemind(t *testing.T) {
	f := GateTemplate()
	r, _ := f.Handle("fechado", Conversation{}, "/menu", t0)
	r, _ = f.Handle("fechado", r.Conversation, "1", t0)

	if _, ok := f.Remind(r.Conversation, t0.Add(2*time.Minute)); ok {
		t.Fatal("reminded before 3 minutes")
	}
	rem, ok := f.Remind(r.Conversation, t0.Add(3*time.Minute))
	if !ok || !strings.Contains(rem.Replies[0], "O portão abriu?") {
		t.Fatalf("reminder = %+v, %v", rem, ok)
	}
	// The next reminder waits another full interval.
	if _, ok := f.Remind(rem.Conversation, t0.Add(5*time.Minute)); ok {
		t.Fatal("reminded twice within the interval")
	}
	if _, ok := f.Remind(Conversation{}, t0.Add(time.Hour)); ok {
		t.Fatal("reminded without a pending question")
	}
}

func TestValidate(t *testing.T) {
	broken := GateTemplate()
	broken.States[0].Options[0].NextState = "nao-existe"
	if broken.Validate() == nil {
		t.Error("accepted an option pointing at a missing state")
	}

	noCommand := GateTemplate()
	noCommand.States[0].Options[0].Command = ""
	if noCommand.Validate() == nil {
		t.Error("accepted a confirmation without a command")
	}

	if (Flow{StartCommand: "/start"}).Validate() == nil {
		t.Error("accepted a flow without states")
	}
}

func pub(r Result) string { return strings.Join(r.Publish, ",") }

func TestAlarmAfterUnansweredReminders(t *testing.T) {
	f := GateTemplate()
	r, _ := f.Handle("fechado", Conversation{}, "/menu", t0)
	r, _ = f.Handle("fechado", r.Conversation, "1", t0)

	// Reminders 1 and 2 only repeat the question.
	c := r.Conversation
	now := t0
	for i := 1; i <= 2; i++ {
		now = now.Add(3 * time.Minute)
		rem, ok := f.Remind(c, now)
		if !ok || pub(rem) != "" {
			t.Fatalf("reminder %d = %+v, %v", i, rem, ok)
		}
		c = rem.Conversation
	}

	// The third unanswered reminder fires the alarm, once.
	now = now.Add(3 * time.Minute)
	rem, _ := f.Remind(c, now)
	if pub(rem) != "ON" || !rem.Conversation.Alarm || len(rem.Replies) != 2 {
		t.Fatalf("third reminder = %+v", rem)
	}
	now = now.Add(3 * time.Minute)
	again, ok := f.Remind(rem.Conversation, now)
	if !ok || pub(again) != "" {
		t.Fatalf("reminder after the alarm = %+v, %v", again, ok)
	}

	// The answer turns the alarm off before handling the confirmation.
	done, _ := f.Handle("fechado", again.Conversation, "1", now)
	if pub(done) != "OFF" || done.NewState != "aberto" || !strings.HasPrefix(done.Replies[0], "Alarme desligado") {
		t.Fatalf("answer after the alarm = %+v", done)
	}

	// Answering "no" also clears it and sends the command again.
	retry, _ := f.Handle("fechado", again.Conversation, "2", now)
	if pub(retry) != "OFF,PULSE" || retry.Conversation.Alarm || retry.Conversation.Reminders != 0 {
		t.Fatalf("no after the alarm = %+v", retry)
	}
}

func TestAlarmDisabled(t *testing.T) {
	f := GateTemplate()
	f.AlarmAfterReminders = 0
	c := Conversation{Step: StepConfirm, StateID: "fechado", Option: 0, Attempts: 1, PromptedAt: t0, Reminders: 10}
	rem, ok := f.Remind(c, t0.Add(3*time.Minute))
	if !ok || pub(rem) != "" {
		t.Fatalf("reminder with the alarm disabled = %+v, %v", rem, ok)
	}

	f.AlarmAfterReminders = 3
	f.AlarmCommand = ""
	if f.Validate() == nil {
		t.Error("accepted an alarm without a command")
	}
}

func TestShortcuts(t *testing.T) {
	f := GateTemplate()

	// /abrir runs the option directly and follows the same confirmation flow.
	r, ok := f.Handle("fechado", Conversation{}, "/ABRIR", t0)
	if !ok || pub(r) != "PULSE" || r.Conversation.Step != StepConfirm || !strings.Contains(r.Replies[0], "O portão abriu?") {
		t.Fatalf("/abrir = %+v, %v", r, ok)
	}
	r, _ = f.Handle("fechado", r.Conversation, "2", t0)
	if pub(r) != "PULSE" || r.Conversation.Attempts != 2 {
		t.Fatalf("no after /abrir = %+v", r)
	}
	r, _ = f.Handle("fechado", r.Conversation, "1", t0)
	if r.NewState != "aberto" {
		t.Fatalf("yes after /abrir = %+v", r)
	}

	// With the gate open, /abrir is not available but /fechar is.
	r, _ = f.Handle("aberto", Conversation{}, "/abrir", t0)
	if pub(r) != "" || !strings.HasPrefix(r.Replies[0], "/abrir não está disponível agora (estado atual: Aberto)") {
		t.Fatalf("/abrir when open = %+v", r)
	}
	r, _ = f.Handle("aberto", Conversation{}, "/fechar", t0)
	if pub(r) != "PULSE" || !strings.Contains(r.Replies[0], "O portão fechou?") {
		t.Fatalf("/fechar = %+v", r)
	}

	// The menu lists the shortcuts; unknown commands are still ignored.
	r, _ = f.Handle("fechado", Conversation{}, "/menu", t0)
	if !strings.Contains(r.Replies[0], "*1* - Abrir o portão (/abrir)") {
		t.Fatalf("menu = %q", r.Replies[0])
	}
	if _, ok := f.Handle("fechado", Conversation{}, "/outro", t0); ok {
		t.Fatal("handled an unknown command")
	}
}

func TestShortcutValidation(t *testing.T) {
	for _, bad := range []string{"abrir", "/ab rir", "/menu", "/"} {
		f := GateTemplate()
		f.States[0].Options[0].Shortcut = bad
		if f.Validate() == nil {
			t.Errorf("accepted shortcut %q", bad)
		}
	}
	f := GateTemplate()
	f.States[0].Options[1].Shortcut = "/abrir"
	if f.Validate() == nil {
		t.Error("accepted a repeated shortcut in the same state")
	}
}
