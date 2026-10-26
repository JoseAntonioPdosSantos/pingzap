package bot

import (
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	"pingzap/internal/flow"
	"pingzap/internal/store"
	"pingzap/internal/whatsapp"
)

func TestDeviceOutage(t *testing.T) {
	s, _, mq, wa, _ := setup(t, store.KindFlow, flow.GateTemplate())
	group := func(text string) whatsapp.Message {
		return whatsapp.Message{Chat: types.NewJID("123", types.GroupServer), Group: "123@g.us", Text: text}
	}

	start := time.Now()
	s.watch(start) // starts counting
	s.HandleTelemetry("x/status", []byte("offline"))
	s.watch(start.Add(silenceLimit))
	if len(wa.sent) != 0 {
		t.Fatalf("warned before the silence limit: %q", wa.sent)
	}

	// Silent for longer than the limit: both chats are told and asked.
	s.watch(start.Add(silenceLimit + 20*time.Second))
	if len(wa.sent) != 2 || !strings.Contains(wa.sent[0], "fora do ar") || !strings.Contains(wa.sent[1], "*2* - Ciente") {
		t.Fatalf("outage warning = %q", wa.sent)
	}
	s.watch(start.Add(silenceLimit + 40*time.Second))
	if len(wa.sent) != 2 {
		t.Fatalf("warned again before outageAskEvery: %q", wa.sent[2:])
	}

	// The contact answers; an invalid answer in the group repeats the
	// question; neither goes to the gate flow.
	s.HandleMessage(contactMsg("2"))
	s.HandleMessage(group("5"))
	if len(wa.sent) != 4 || !strings.Contains(wa.sent[2], "Ciência registrada") ||
		!strings.HasPrefix(wa.sent[3], "Não entendi") {
		t.Fatalf("answers = %q", wa.sent[2:])
	}
	if len(mq.published) != 0 {
		t.Fatalf("the answer reached the flow: %q", mq.published)
	}

	// Only the group, still without a valid answer, is asked again.
	s.watch(start.Add(silenceLimit + 20*time.Second + outageAskEvery))
	if len(wa.sent) != 5 || !strings.Contains(wa.sent[4], "fora do ar") {
		t.Fatalf("reminder = %q", wa.sent[4:])
	}
	s.HandleMessage(group("1"))
	if last := wa.sent[len(wa.sent)-1]; !strings.Contains(last, "em análise") {
		t.Fatalf("answer 1 = %q", last)
	}
	s.watch(start.Add(silenceLimit + 20*time.Second + 2*outageAskEvery))
	if len(wa.sent) != 6 {
		t.Fatalf("asked after a valid answer: %q", wa.sent[6:])
	}

	// The device is back: everyone hears it, and the flow works again.
	s.HandleTelemetry("x/data", []byte(`{"uptime_s": 3}`))
	if len(wa.sent) != 8 || !strings.Contains(wa.sent[6], "voltou a responder") {
		t.Fatalf("back = %q", wa.sent[6:])
	}
	s.HandleMessage(contactMsg("/start"))
	s.HandleMessage(contactMsg("1"))
	if len(mq.published) != 1 || mq.published[0] != "x/cmd PULSE" {
		t.Fatalf("published = %q", mq.published)
	}
}

func TestDuration(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "1 min", 90 * time.Second: "2 min", 59 * time.Minute: "59 min",
		125 * time.Minute: "2 h 05 min",
	} {
		if got := duration(d); got != want {
			t.Errorf("duration(%s) = %q, want %q", d, got, want)
		}
	}
}
