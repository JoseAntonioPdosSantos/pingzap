package bot

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	"pingzap/internal/config"
	"pingzap/internal/flow"
	"pingzap/internal/store"
	"pingzap/internal/whatsapp"
)

type fakeMQTT struct{ published []string }

func (f *fakeMQTT) Publish(topic, payload string) error {
	f.published = append(f.published, topic+" "+payload)
	return nil
}
func (f *fakeMQTT) SetSubscriptions([]string) {}

type fakeSender struct{ sent []string }

func (f *fakeSender) SendText(_ context.Context, _ types.JID, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

func setup(t *testing.T, kind string, cfg any) (*Service, *store.Store, *fakeMQTT, *fakeSender, store.Bot) {
	t.Helper()
	st, err := store.Open("file::memory:?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dev, err := st.CreateDevice(store.Device{Name: "ESP", ClientID: "esp", CmdTopic: "x/cmd", DataTopic: "x/data", StatusTopic: "x/status"})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(cfg)
	b, err := st.SaveBot(store.Bot{Name: "bot", Kind: kind, Enabled: true, DeviceID: &dev.ID, Config: raw,
		Targets: []store.Target{
			{Kind: store.TargetContact, Value: "5567999999999"},
			{Kind: store.TargetGroup, Value: "123@g.us"},
		}})
	if err != nil {
		t.Fatal(err)
	}
	mq, wa := &fakeMQTT{}, &fakeSender{}
	s := New(st, mq, wa, nil)
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	return s, st, mq, wa, b
}

// The contact is configured with the ninth digit and writes without it.
func contactMsg(text string) whatsapp.Message {
	return whatsapp.Message{Chat: types.NewJID("556799999999", types.DefaultUserServer), Phone: "556799999999", Text: text}
}

func TestFlowBot(t *testing.T) {
	s, st, mq, wa, b := setup(t, store.KindFlow, flow.GateTemplate())

	s.HandleMessage(contactMsg("oi"))
	if len(wa.sent) != 0 {
		t.Fatalf("answered a message outside the flow: %q", wa.sent)
	}

	s.HandleMessage(contactMsg("/start"))
	s.HandleMessage(contactMsg("1"))
	if len(mq.published) != 1 || mq.published[0] != "x/cmd PULSE" {
		t.Fatalf("published = %q", mq.published)
	}

	// My own messages that are not input (e.g. the bot's own replies, sent
	// from my account) are ignored.
	sentBefore := len(wa.sent)
	mine := contactMsg("O portão abriu?\n*1* - Sim\n*2* - Não")
	mine.FromMe = true
	s.HandleMessage(mine)
	if len(wa.sent) != sentBefore || len(mq.published) != 1 {
		t.Fatalf("answered my own non-input message: %q %q", wa.sent[sentBefore:], mq.published)
	}

	// Unanswered for 3 minutes: the question is repeated.
	sent := len(wa.sent)
	s.remind(time.Now().Add(3 * time.Minute))
	if len(wa.sent) != sent+1 || !strings.Contains(wa.sent[sent], "O portão abriu?") {
		t.Fatalf("reminder: %q", wa.sent[sent:])
	}

	s.HandleMessage(contactMsg("1"))
	if got, _ := st.GetBot(b.ID); got.CurrentState != "aberto" {
		t.Fatalf("state = %q, want aberto", got.CurrentState)
	}

	// The state is shared: the group now sees the option to close.
	s.HandleMessage(whatsapp.Message{Chat: types.NewJID("123", types.GroupServer), Group: "123@g.us", Text: "/menu"})
	if last := wa.sent[len(wa.sent)-1]; !strings.Contains(last, "Fechar o portão") {
		t.Fatalf("group menu = %q", last)
	}

	// Strangers are ignored.
	s.HandleMessage(whatsapp.Message{Chat: types.NewJID("5511912345678", types.DefaultUserServer), Phone: "5511912345678", Text: "/start"})
	if last := wa.sent[len(wa.sent)-1]; !strings.Contains(last, "Fechar o portão") {
		t.Fatalf("answered a stranger: %q", last)
	}
}

func TestAlertBot(t *testing.T) {
	s, _, mq, _, _ := setup(t, store.KindAlert, AlertConfig{Keyword: "/urgente", OnPayload: "ON", OffPayload: "OFF"})

	s.HandleMessage(contactMsg("oi"))
	s.HandleMessage(contactMsg("me liga /URGENTE"))
	s.HandleMessage(whatsapp.Message{Chat: types.NewJID("123", types.GroupServer), Group: "123@g.us", Text: "/urgente"})

	reply := contactMsg("já te ligo")
	reply.FromMe = true
	s.HandleMessage(reply) // the group still waits: alert kept on

	groupReply := whatsapp.Message{Chat: types.NewJID("123", types.GroupServer), Group: "123@g.us", FromMe: true}
	s.HandleMessage(groupReply)

	want := []string{"x/cmd ON", "x/cmd ON", "x/cmd OFF"}
	if strings.Join(mq.published, ",") != strings.Join(want, ",") {
		t.Fatalf("published = %q, want %q", mq.published, want)
	}
}

func TestImportLegacy(t *testing.T) {
	st, err := store.Open("file::memory:?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	l := config.Legacy{Numbers: []string{"67999999999", "+55 (67) 99999-9999"}, Keyword: "/urgente",
		CmdTopic: "esp32/unit01/cmd", DataTopic: "esp32/unit01/data", OnPayload: "ON", OffPayload: "OFF"}
	for range 2 { // the second run must not import again
		if err := ImportLegacy(st, l); err != nil {
			t.Fatal(err)
		}
	}
	bots, _ := st.ListBots()
	devices, _ := st.ListDevices()
	if len(bots) != 1 || len(devices) != 1 || len(bots[0].Targets) != 1 {
		t.Fatalf("bots = %+v, devices = %+v", bots, devices)
	}
	if devices[0].StatusTopic != "esp32/unit01/status" {
		t.Fatalf("status topic = %q", devices[0].StatusTopic)
	}
}

func TestMatchesKeyword(t *testing.T) {
	cases := []struct {
		text, keyword string
		want          bool
	}{
		{"qualquer coisa", "", true},
		{"", "", true},
		{"/urgente", "/urgente", true},
		{"Me liga, /URGENTE!", "/urgente", true},
		{"oi, tudo bem?", "/urgente", false},
		{"", "/urgente", false},
	}
	for _, c := range cases {
		if got := matchesKeyword(c.text, c.keyword); got != c.want {
			t.Errorf("matchesKeyword(%q, %q) = %v, want %v", c.text, c.keyword, got, c.want)
		}
	}
}

func TestFlowBotAlarm(t *testing.T) {
	s, _, mq, wa, _ := setup(t, store.KindFlow, flow.GateTemplate())
	s.HandleMessage(contactMsg("/start"))
	s.HandleMessage(contactMsg("1"))

	start := time.Now()
	for i := 1; i <= 3; i++ {
		s.remind(start.Add(time.Duration(3*i) * time.Minute))
	}
	want := []string{"x/cmd PULSE", "x/cmd ON"}
	if strings.Join(mq.published, ",") != strings.Join(want, ",") {
		t.Fatalf("published = %q, want %q", mq.published, want)
	}
	if last := wa.sent[len(wa.sent)-1]; !strings.Contains(last, "acionei o alarme") {
		t.Fatalf("alarm message = %q", last)
	}

	s.HandleMessage(contactMsg("1"))
	if got := mq.published[len(mq.published)-1]; got != "x/cmd OFF" {
		t.Fatalf("after the answer published %q, want x/cmd OFF", got)
	}
}

// The owner of the paired account uses the bot from their own phone.
func TestFlowBotFromMe(t *testing.T) {
	s, _, mq, wa, _ := setup(t, store.KindFlow, flow.GateTemplate())
	group := func(text string) whatsapp.Message {
		return whatsapp.Message{Chat: types.NewJID("123", types.GroupServer), Group: "123@g.us", FromMe: true, Text: text}
	}
	s.HandleMessage(group("/start"))
	s.HandleMessage(group("1"))
	if len(wa.sent) != 2 || len(mq.published) != 1 || mq.published[0] != "x/cmd PULSE" {
		t.Fatalf("sent = %q, published = %q", wa.sent, mq.published)
	}
}

func TestIsInput(t *testing.T) {
	for text, want := range map[string]bool{
		"/start": true, " 1 ": true, "2": true, "/menu agora": false,
		"Comando enviado.\n\nO portão abriu?": false, "oi": false, "": false,
	} {
		if got := isInput(text); got != want {
			t.Errorf("isInput(%q) = %v, want %v", text, got, want)
		}
	}
}
