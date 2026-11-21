package bot

import (
	"errors"
	"strings"
	"testing"
	"time"

	"pingzap/internal/flow"
	"pingzap/internal/store"
)

func newPass(t *testing.T, st *store.Store, botID int64, in store.PassInput) string {
	t.Helper()
	p, err := st.CreatePass(botID, in)
	if err != nil {
		t.Fatal(err)
	}
	return p.Token
}

// A pass for one open and one close: after both it is over.
func TestPassOpenThenCloseThenExpired(t *testing.T) {
	s, st, mq, wa, b := setup(t, store.KindFlow, flow.GateTemplate())
	token := newPass(t, st, b.ID, store.PassInput{Name: "Visita", MaxUses: 2})

	view, err := s.Remote(token)
	if err != nil {
		t.Fatal(err)
	}
	if view.Owner != "Visita" || view.Access == nil || view.Access.UsesLeft != 2 || !view.Buttons[0].Enabled {
		t.Fatalf("view = %+v access = %+v", view, view.Access)
	}

	sent := len(wa.sent)
	if view, err = s.RemoteAct(token, "fechado", 0, false); err != nil || view.State.ID != "aberto" || view.Access.UsesLeft != 1 {
		t.Fatalf("open: state = %q, access = %+v, err = %v", view.State.ID, view.Access, err)
	}
	// Sending again what was just sent is free.
	if view, err = s.RemoteAct(token, "fechado", 0, true); err != nil || view.Access.UsesLeft != 1 {
		t.Fatalf("retry: access = %+v, err = %v", view.Access, err)
	}
	if view, err = s.RemoteAct(token, "aberto", 0, false); err != nil || view.State.ID != "fechado" || view.Access.UsesLeft != 0 {
		t.Fatalf("close: state = %q, access = %+v, err = %v", view.State.ID, view.Access, err)
	}
	s.sending.Wait()
	if len(mq.published) != 3 {
		t.Fatalf("published = %q", mq.published)
	}
	if len(wa.sent) != sent {
		t.Fatalf("WhatsApp told %q", wa.sent[sent:])
	}

	// Right after the last use only a retry of that command is allowed, and
	// nothing else is enabled.
	view, err = s.Remote(token)
	if err != nil || view.Access.Note == "" || view.Retry == nil {
		t.Fatalf("last chance: %+v, err = %v", view, err)
	}
	for _, b := range view.Buttons {
		if b.Enabled {
			t.Fatalf("button enabled after the last use: %+v", b)
		}
	}
	var locked *RemoteLocked
	if _, err := s.RemoteAct(token, "fechado", 0, false); !errors.As(err, &locked) || !locked.Permanent {
		t.Fatalf("new command after the last use: %v", err)
	}
	if len(mq.published) != 3 {
		t.Fatalf("published after the limit: %q", mq.published)
	}

	// Once the retry window passes, the link is over.
	uses, _ := st.RemoteUses(b.ID, 10)
	if len(uses) != 3 || uses[0].Phone != "" || uses[0].Name != "Visita" {
		t.Fatalf("history = %+v", uses)
	}
	if _, _, err := s.remoteRoute(token, time.Now().Add(retryWindow+time.Second)); !errors.As(err, &locked) || !locked.Permanent {
		t.Fatalf("after the window: err = %v", err)
	}
}

func TestPassExpiredAndNotStarted(t *testing.T) {
	s, st, mq, _, b := setup(t, store.KindFlow, flow.GateTemplate())
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)

	expired := newPass(t, st, b.ID, store.PassInput{Name: "Ontem", ExpiresAt: &past})
	var locked *RemoteLocked
	if _, err := s.Remote(expired); !errors.As(err, &locked) || !locked.Permanent {
		t.Fatalf("expired: %v", err)
	}
	if _, err := s.RemoteAct(expired, "fechado", 0, false); !errors.As(err, &locked) {
		t.Fatalf("act on expired: %v", err)
	}

	later := newPass(t, st, b.ID, store.PassInput{Name: "Amanhã", StartsAt: &future})
	if _, err := s.Remote(later); !errors.As(err, &locked) || locked.Permanent {
		t.Fatalf("not started: err = %v, want a temporary lock", err)
	}
	if len(mq.published) != 0 {
		t.Fatalf("published = %q", mq.published)
	}
}

func TestPassOutsideHours(t *testing.T) {
	s, st, mq, _, b := setup(t, store.KindFlow, flow.GateTemplate())
	now := time.Now().UTC()
	// A window that is certainly not now: the other half of the day, today.
	start := now.Add(12 * time.Hour)
	win := store.Window{Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: start.Format("15:04"), End: start.Add(time.Hour).Format("15:04")}
	token := newPass(t, st, b.ID, store.PassInput{Name: "Faxina", Timezone: "UTC", Schedule: store.Schedule{Windows: []store.Window{win}}})

	var locked *RemoteLocked
	if _, err := s.RemoteAct(token, "fechado", 0, false); !errors.As(err, &locked) || locked.Permanent {
		t.Fatalf("outside hours: err = %v, want a temporary lock", err)
	}
	if len(mq.published) != 0 {
		t.Fatalf("published outside hours: %q", mq.published)
	}
	all := store.Window{Days: []int{0, 1, 2, 3, 4, 5, 6}, Start: "00:00", End: "24:00"}
	open := newPass(t, st, b.ID, store.PassInput{Name: "Sempre", Timezone: "UTC", Schedule: store.Schedule{Windows: []store.Window{all}}})
	if _, err := s.RemoteAct(open, "fechado", 0, false); err != nil {
		t.Fatalf("inside hours: %v", err)
	}
}

func TestPassRevoked(t *testing.T) {
	s, st, _, _, b := setup(t, store.KindFlow, flow.GateTemplate())
	p, _ := st.CreatePass(b.ID, store.PassInput{Name: "Visita", MaxUses: 1})
	if err := st.DeletePass(b.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remote(p.Token); !errors.Is(err, ErrRemoteGone) {
		t.Fatalf("revoked: %v", err)
	}
}

func TestSendPassLink(t *testing.T) {
	s, st, _, wa, b := setup(t, store.KindFlow, flow.GateTemplate())
	exp := time.Now().Add(24 * time.Hour)
	p, err := st.CreatePass(b.ID, store.PassInput{Name: "Maria", MaxUses: 2, ExpiresAt: &exp, Timezone: "America/Manaus",
		Schedule: store.Schedule{Windows: []store.Window{{Days: []int{1, 2}, Start: "08:00", End: "18:00"}}}})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SendPassLink(p, "(67) 99999-1234"); err != nil {
		t.Fatal(err)
	}
	if len(wa.sent) != 1 || wa.to[0] != "5567999991234@s.whatsapp.net" {
		t.Fatalf("sent %q to %q", wa.sent, wa.to)
	}
	for _, want := range []string{"Maria", "https://portao.example/c/" + p.Token, "Limite de 2 usos", "Vale até", "seg, ter das 08:00 às 18:00"} {
		if !strings.Contains(wa.sent[0], want) {
			t.Errorf("message lacks %q:\n%s", want, wa.sent[0])
		}
	}

	var ve *store.ValidationError
	if err := s.SendPassLink(p, "123"); !errors.As(err, &ve) {
		t.Fatalf("short number: %v", err)
	}
	s.publicURL = ""
	if err := s.SendPassLink(p, "67999991234"); !errors.Is(err, ErrNoPublicURL) {
		t.Fatalf("no public URL: %v", err)
	}
}
