package flow

import (
	"strings"
	"testing"
)

func TestGateTemplateIsValid(t *testing.T) {
	if err := GateTemplate().Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestOpenGate(t *testing.T) {
	f := GateTemplate()

	// Messages outside a conversation are ignored.
	if _, ok := f.Handle("fechado", Conversation{}, "oi"); ok {
		t.Fatal("plain message handled without a conversation")
	}

	r, ok := f.Handle("fechado", Conversation{}, " /START ")
	if !ok || r.Conversation.Step != StepMenu || !strings.Contains(r.Replies[0], "*1* - Abrir o portão") {
		t.Fatalf("/start = %+v, %v", r, ok)
	}

	// The command is trusted: the state changes at once, no question asked,
	// and the next step (close) comes before "try again".
	r, _ = f.Handle("fechado", r.Conversation, "1")
	want := "Comando enviado: portão *aberto*.\n\nDigite o número da opção:\n*1* - Fechar o portão (/fechar)\n*2* - Tentar novamente"
	if pub(r) != "PULSE" || r.NewState != "aberto" || r.Conversation.Step != StepSent || r.Replies[0] != want {
		t.Fatalf("option 1 = %+v", r)
	}
	sent := r.Conversation

	// "Try again" sends the command again and keeps the state.
	r, _ = f.Handle("aberto", sent, "2")
	if pub(r) != "PULSE" || r.NewState != "" || r.Conversation.Attempts != 2 || !strings.Contains(r.Replies[0], "*2* - Tentar novamente") {
		t.Fatalf("try again = %+v", r)
	}

	// "Close" runs the close option, which only offers "try again".
	r, _ = f.Handle("aberto", sent, "1")
	want = "Comando enviado: portão *fechado*.\n\nDigite o número da opção:\n*1* - Tentar novamente"
	if pub(r) != "PULSE" || r.NewState != "fechado" || r.Conversation.Step != StepSent || r.Replies[0] != want {
		t.Fatalf("close after opening = %+v", r)
	}

	r, _ = f.Handle("fechado", r.Conversation, "1")
	if pub(r) != "PULSE" || r.NewState != "" || r.Conversation.Attempts != 2 {
		t.Fatalf("try closing again = %+v", r)
	}
}

func TestAfterCommandInput(t *testing.T) {
	f := GateTemplate()
	r, _ := f.Handle("fechado", Conversation{}, "/abrir")
	sent := r.Conversation

	// Nothing was asked: plain chat stays quiet and keeps the options.
	if r, ok := f.Handle("aberto", sent, "valeu"); ok || r.Conversation != sent {
		t.Fatalf("plain text after a command = %+v, %v", r, ok)
	}

	inv, _ := f.Handle("aberto", sent, "3")
	if pub(inv) != "" || inv.Conversation != sent || !strings.HasPrefix(inv.Replies[0], "Opção inválida") {
		t.Fatalf("option 3 = %+v", inv)
	}

	// Someone closed the gate meanwhile: trying again must not reopen it.
	stale, _ := f.Handle("fechado", sent, "2")
	if pub(stale) != "" || stale.Conversation.Step != StepMenu || !strings.HasPrefix(stale.Replies[0], "O estado mudou") {
		t.Fatalf("stale retry = %+v", stale)
	}

	// Saved conversations from the old confirmation step are ignored.
	old := Conversation{Step: "confirm", StateID: "fechado", Attempts: 1}
	if _, ok := f.Handle("fechado", old, "1"); ok {
		t.Fatal("handled an old confirmation")
	}
}

func TestReportClosedByRemote(t *testing.T) {
	f := GateTemplate()
	r, _ := f.Handle("aberto", Conversation{}, "/menu")
	r, _ = f.Handle("aberto", r.Conversation, "2")
	if r.NewState != "fechado" || pub(r) != "" || r.Conversation.Step != StepIdle {
		t.Fatalf("report closed = %+v", r)
	}
}

func TestMaxAttempts(t *testing.T) {
	f := GateTemplate()
	f.MaxAttempts = 2
	r, _ := f.Handle("aberto", Conversation{}, "/fechar") // attempt 1
	r, _ = f.Handle("fechado", r.Conversation, "1")       // attempt 2
	if pub(r) != "PULSE" || !strings.HasPrefix(r.Replies[0], "Comando enviado novamente (tentativa 2).\n\nLimite de 2") || r.Conversation.Step != StepIdle {
		t.Fatalf("last attempt = %+v", r)
	}

	// With other options to offer, they remain after the limit.
	r, _ = f.Handle("fechado", Conversation{}, "/abrir")
	r, _ = f.Handle("aberto", r.Conversation, "2")
	if !strings.Contains(r.Replies[0], "*1* - Fechar o portão") || strings.Contains(r.Replies[0], "Tentar novamente") {
		t.Fatalf("last attempt with extras = %q", r.Replies[0])
	}
	again, _ := f.Handle("aberto", r.Conversation, "2")
	if pub(again) != "" || !strings.HasPrefix(again.Replies[0], "Opção inválida") {
		t.Fatalf("retry past the limit = %+v", again)
	}
}

func TestInvalidOptionAndStaleMenu(t *testing.T) {
	f := GateTemplate()
	r, _ := f.Handle("fechado", Conversation{}, "/menu")

	inv, _ := f.Handle("fechado", r.Conversation, "9")
	if pub(inv) != "" || inv.Conversation.Step != StepMenu || !strings.HasPrefix(inv.Replies[0], "Opção inválida") {
		t.Fatalf("invalid option = %+v", inv)
	}

	// Someone else opened the gate meanwhile: option 1 must not close it.
	stale, _ := f.Handle("aberto", r.Conversation, "1")
	if pub(stale) != "" || stale.Conversation.StateID != "aberto" {
		t.Fatalf("stale menu = %+v", stale)
	}
}

func TestValidate(t *testing.T) {
	broken := GateTemplate()
	broken.States[0].Options[0].NextState = "nao-existe"
	if broken.Validate() == nil {
		t.Error("accepted an option pointing at a missing state")
	}

	negative := GateTemplate()
	negative.MaxAttempts = -1
	if negative.Validate() == nil {
		t.Error("accepted negative attempts")
	}

	if (Flow{StartCommand: "/start"}).Validate() == nil {
		t.Error("accepted a flow without states")
	}
}

func pub(r Result) string { return strings.Join(r.Publish, ",") }

func TestShortcuts(t *testing.T) {
	f := GateTemplate()

	r, ok := f.Handle("fechado", Conversation{}, "/ABRIR")
	if !ok || pub(r) != "PULSE" || r.NewState != "aberto" || r.Conversation.Step != StepSent {
		t.Fatalf("/abrir = %+v, %v", r, ok)
	}

	// A shortcut also works while the options after a command are shown.
	r, _ = f.Handle("aberto", r.Conversation, "/fechar")
	if pub(r) != "PULSE" || r.NewState != "fechado" {
		t.Fatalf("/fechar after /abrir = %+v", r)
	}

	// With the gate open, /abrir is not available but /fechar is.
	r, _ = f.Handle("aberto", Conversation{}, "/abrir")
	if pub(r) != "" || !strings.HasPrefix(r.Replies[0], "/abrir não está disponível agora (estado atual: Aberto)") {
		t.Fatalf("/abrir when open = %+v", r)
	}

	// The menu lists the shortcuts; unknown commands are still ignored.
	r, _ = f.Handle("fechado", Conversation{}, "/menu")
	if !strings.Contains(r.Replies[0], "*1* - Abrir o portão (/abrir)") {
		t.Fatalf("menu = %q", r.Replies[0])
	}
	if _, ok := f.Handle("fechado", Conversation{}, "/outro"); ok {
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

func TestOptionWithoutCommand(t *testing.T) {
	f := GateTemplate()
	// An option offered after a command may only record the state.
	f.States[0].Options[1].ShowAfterCommand = true

	r, _ := f.Handle("aberto", Conversation{}, "/fechar")
	if !strings.Contains(r.Replies[0], "*1* - Informar que o portão foi aberto pelo controle\n*2* - Tentar novamente") {
		t.Fatalf("options = %q", r.Replies[0])
	}
	done, _ := f.Handle("fechado", r.Conversation, "1")
	if pub(done) != "" || done.NewState != "aberto" || done.Conversation.Step != StepIdle {
		t.Fatalf("option 1 = %+v", done)
	}
}
