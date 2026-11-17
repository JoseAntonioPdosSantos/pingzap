package bot

import (
	"errors"
	"strings"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"

	"pingzap/internal/flow"
	"pingzap/internal/store"
	"pingzap/internal/whatsapp"
)

const groupJID = "123@g.us"

// groupMsg is a message written in the bot's group by the given number.
func groupMsg(sender, name, text string) whatsapp.Message {
	chat, _ := types.ParseJID(groupJID)
	return whatsapp.Message{Chat: chat, Group: groupJID, Sender: sender, SenderName: name, Text: text}
}

// remoteToken asks for the web remote in the group and returns the token of
// the link sent in private.
func remoteToken(t *testing.T, s *Service, wa *fakeSender, sender, name string) string {
	t.Helper()
	before := len(wa.sent)
	s.HandleMessage(groupMsg(sender, name, "/controle"))
	if len(wa.sent) != before+2 {
		t.Fatalf("sent %d message(s), want the link and the note: %q", len(wa.sent)-before, wa.sent[before:])
	}
	if want := sender + "@s.whatsapp.net"; wa.to[before] != want {
		t.Fatalf("link sent to %s, want the private chat %s", wa.to[before], want)
	}
	if wa.to[before+1] != groupJID || strings.Contains(wa.sent[before+1], "/c/") {
		t.Fatalf("group note = %q to %s", wa.sent[before+1], wa.to[before+1])
	}
	_, token, ok := strings.Cut(wa.sent[before], "https://portao.example/c/")
	if !ok {
		t.Fatalf("no link in %q", wa.sent[before])
	}
	token, _, _ = strings.Cut(token, "\n")
	return token
}

func TestRemoteLinkIsPersonalAndStable(t *testing.T) {
	s, _, _, wa, _ := setup(t, store.KindFlow, flow.GateTemplate())

	ana := remoteToken(t, s, wa, "5567911111111", "Ana")
	if len(ana) < 32 {
		t.Fatalf("token %q is too short to resist guessing", ana)
	}
	if again := remoteToken(t, s, wa, "5567911111111", "Ana"); again != ana {
		t.Fatal("asking again created a different link")
	}
	if bia := remoteToken(t, s, wa, "5567922222222", "Bia"); bia == ana {
		t.Fatal("two people got the same link")
	}

	// The menu offers the remote as its last option.
	before := len(wa.sent)
	s.HandleMessage(groupMsg("5567911111111", "Ana", "/menu"))
	if !strings.Contains(wa.sent[before], "*3* - "+flow.RemoteLabel+" (/controle)") {
		t.Fatalf("menu = %q", wa.sent[before])
	}
	s.HandleMessage(groupMsg("5567911111111", "Ana", "3"))
	if got := wa.sent[len(wa.sent)-2]; !strings.Contains(got, "/c/"+ana) {
		t.Fatalf("menu option sent %q, want the same link", got)
	}
}

func TestRemoteAct(t *testing.T) {
	s, st, mq, wa, b := setup(t, store.KindFlow, flow.GateTemplate())
	token := remoteToken(t, s, wa, "5567911111111", "Ana")

	view, err := s.Remote(token)
	if err != nil {
		t.Fatal(err)
	}
	if view.Owner != "Ana" || view.State.ID != "fechado" || len(view.Buttons) != 2 || len(view.Fixes) != 1 {
		t.Fatalf("view = %+v", view)
	}
	if !view.Buttons[0].Enabled || view.Buttons[1].Enabled || view.Retry != nil {
		t.Fatalf("buttons = %+v, retry = %v", view.Buttons, view.Retry)
	}

	sentBefore := len(wa.sent)
	view, err = s.RemoteAct(token, "fechado", 0, false)
	if err != nil {
		t.Fatal(err)
	}
	s.sending.Wait()
	if len(mq.published) != 1 || mq.published[0] != "x/cmd PULSE" {
		t.Fatalf("published = %q", mq.published)
	}
	if got, _ := st.GetBot(b.ID); got.CurrentState != "aberto" || view.State.ID != "aberto" {
		t.Fatalf("state = %q, view = %q", got.CurrentState, view.State.ID)
	}
	// The use is only recorded: nothing is sent to WhatsApp.
	if len(wa.sent) != sentBefore {
		t.Fatalf("WhatsApp told %q", wa.sent[sentBefore:])
	}
	uses, _ := st.RemoteUses(b.ID, 10)
	if len(uses) != 1 || uses[0].Phone != "5567911111111" || uses[0].Action != "Abrir o portão" || uses[0].State != "Aberto" {
		t.Fatalf("uses = %+v", uses)
	}
	links, _ := st.RemoteLinks(b.ID)
	if len(links) != 1 || links[0].Uses != 1 || links[0].LastUsed == nil {
		t.Fatalf("links = %+v", links)
	}

	// A second press of the same button (double tap, or a page that did not
	// refresh) must not pulse the relay again.
	if _, err := s.RemoteAct(token, "fechado", 0, false); !errors.Is(err, ErrRemoteStale) {
		t.Fatalf("stale press: err = %v", err)
	}
	if len(mq.published) != 1 {
		t.Fatalf("stale press published: %q", mq.published)
	}

	// The command just sent can be sent again, with no limit.
	if view.Retry == nil || view.Retry.Label != "Abrir o portão" {
		t.Fatalf("retry = %+v", view.Retry)
	}
	for attempt := 2; attempt <= 5; attempt++ {
		if view, err = s.RemoteAct(token, "fechado", 0, true); err != nil {
			t.Fatalf("retry %d: %v", attempt, err)
		}
		if view.Retry == nil {
			t.Fatalf("retry no longer offered after attempt %d", attempt)
		}
	}
	if len(mq.published) != 5 {
		t.Fatalf("published = %q", mq.published)
	}

	// An option without a command only corrects the state.
	if view, err = s.RemoteAct(token, "aberto", 1, false); err != nil || view.State.ID != "fechado" {
		t.Fatalf("fix: state = %q, err = %v", view.State.ID, err)
	}
	if len(mq.published) != 5 {
		t.Fatalf("fix published: %q", mq.published)
	}
	s.sending.Wait()
}

func TestRemoteLinkStopsWorking(t *testing.T) {
	s, st, _, wa, b := setup(t, store.KindFlow, flow.GateTemplate())
	token := remoteToken(t, s, wa, "5567911111111", "Ana")

	for _, bad := range []string{"", "x", token + "x", token[:len(token)-1]} {
		if _, err := s.Remote(bad); !errors.Is(err, ErrRemoteGone) {
			t.Fatalf("token %q: err = %v", bad, err)
		}
	}

	// The group leaves the bot: links asked for there die with it.
	saved, _ := st.GetBot(b.ID)
	saved.Targets = saved.Targets[:1]
	if _, err := st.SaveBot(saved); err != nil {
		t.Fatal(err)
	}
	s.Reload()
	if _, err := s.RemoteAct(token, "fechado", 0, false); !errors.Is(err, ErrRemoteGone) {
		t.Fatalf("after the group left: err = %v", err)
	}

	// Revoked: gone, and the next request gets a new link.
	saved.Targets = append(saved.Targets, store.Target{Kind: store.TargetGroup, Value: groupJID})
	if _, err := st.SaveBot(saved); err != nil {
		t.Fatal(err)
	}
	s.Reload()
	if _, err := s.Remote(token); err != nil {
		t.Fatalf("back in the group: %v", err)
	}
	links, _ := st.RemoteLinks(b.ID)
	if err := st.DeleteRemoteLink(b.ID, links[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Remote(token); !errors.Is(err, ErrRemoteGone) {
		t.Fatalf("revoked: err = %v", err)
	}
	if fresh := remoteToken(t, s, wa, "5567911111111", "Ana"); fresh == token {
		t.Fatal("revoked link was reused")
	}
}

// Only people still in the group may use a link asked for there.
func TestRemoteNeedsGroupMembership(t *testing.T) {
	s, _, mq, wa, _ := setup(t, store.KindFlow, flow.GateTemplate())
	token := remoteToken(t, s, wa, "5567911111111", "Ana")
	start := time.Now()
	at := func(d time.Duration) error {
		_, _, err := s.remoteRoute(token, start.Add(d))
		return err
	}
	if err := at(0); err != nil {
		t.Fatalf("member: %v", err)
	}

	// Ana leaves the group: the remote stops once the list is refreshed.
	wa.members = []string{"5567922222222"}
	if err := at(membersFresh + time.Second); !errors.Is(err, ErrRemoteGone) {
		t.Fatalf("after leaving: err = %v", err)
	}
	s.members = map[string]groupMembers{}
	if _, err := s.RemoteAct(token, "fechado", 0, false); !errors.Is(err, ErrRemoteGone) || len(mq.published) != 0 {
		t.Fatalf("command after leaving: err = %v, published = %q", err, mq.published)
	}

	// Back in the group, the same link works again.
	wa.members = []string{"5567911111111"}
	if err := at(3 * membersFresh); err != nil {
		t.Fatalf("back in the group: %v", err)
	}

	// WhatsApp offline: the last list counts for a while, then nobody is
	// confirmed and the remote waits.
	wa.membersErr = errors.New("offline")
	if err := at(3*membersFresh + membersStale - time.Second); err != nil {
		t.Fatalf("offline with a recent list: %v", err)
	}
	if err := at(3*membersFresh + membersStale + time.Second); !errors.Is(err, ErrRemoteUnverified) {
		t.Fatalf("offline with an old list: err = %v", err)
	}
}

func TestRemoteOff(t *testing.T) {
	f := flow.GateTemplate()
	f.RemoteCommand = ""
	s, _, _, wa, _ := setup(t, store.KindFlow, f)
	s.HandleMessage(groupMsg("5567911111111", "Ana", "/controle"))
	if len(wa.sent) != 0 {
		t.Fatalf("answered with the remote off: %q", wa.sent)
	}
	s.HandleMessage(groupMsg("5567911111111", "Ana", "/menu"))
	if strings.Contains(wa.sent[0], flow.RemoteLabel) {
		t.Fatalf("menu offers the remote: %q", wa.sent[0])
	}
}
