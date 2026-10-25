package store

import (
	"encoding/json"
	"errors"
	"testing"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open("file::memory:?_foreign_keys=on")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUsersAndSessions(t *testing.T) {
	s := open(t)
	if _, err := s.CreateUser("admin", "curta"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("weak password: %v", err)
	}
	u, err := s.CreateUser("admin", "senha-forte")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("admin", "outra-senha"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate user: %v", err)
	}
	if _, _, err := s.Login("admin", "errada!!"); !errors.Is(err, ErrInvalidLogin) {
		t.Fatalf("wrong password: %v", err)
	}

	token, _, err := s.Login("admin", "senha-forte")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionUser(token); err != nil || got.ID != u.ID {
		t.Fatalf("SessionUser = %+v, %v", got, err)
	}

	// Changing the password ends the sessions.
	if err := s.SetPassword(u.ID, "nova-senha-forte"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("session after password change: %v", err)
	}
	if err := s.DeleteUser(u.ID); !errors.Is(err, ErrLastUser) {
		t.Fatalf("delete last user: %v", err)
	}
}

func TestBotsTargetsAndCascade(t *testing.T) {
	s := open(t)
	dev, err := s.CreateDevice(Device{Name: "Portão", ClientID: "esp-1", CmdTopic: "a/cmd", DataTopic: "a/data", StatusTopic: "a/status"})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.DeviceByToken(dev.Token); err != nil || got.ID != dev.ID {
		t.Fatalf("DeviceByToken = %+v, %v", got, err)
	}

	cfg := json.RawMessage(`{}`)
	bot, err := s.SaveBot(Bot{Name: "Portão", Kind: KindFlow, Enabled: true, DeviceID: &dev.ID, Config: cfg,
		Targets: []Target{{Kind: TargetContact, Value: "5567999999999", Label: "Ana"}}})
	if err != nil {
		t.Fatal(err)
	}

	// A chat belongs to one bot only.
	_, err = s.SaveBot(Bot{Name: "Outro", Kind: KindAlert, Config: cfg,
		Targets: []Target{{Kind: TargetContact, Value: "5567999999999"}}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("shared target: %v", err)
	}

	if err := s.SaveConversation(bot.ID, "chat", []byte(`{"step":"menu"}`)); err != nil {
		t.Fatal(err)
	}
	// Saving the bot resets its conversations.
	if _, err := s.SaveBot(bot); err != nil {
		t.Fatal(err)
	}
	if data, _ := s.Conversation(bot.ID, "chat"); data != nil {
		t.Fatalf("conversation survived a bot update: %s", data)
	}

	// Deleting the device detaches it from the bot.
	if err := s.DeleteDevice(dev.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetBot(bot.ID); got.DeviceID != nil || len(got.Targets) != 1 {
		t.Fatalf("bot after device delete = %+v", got)
	}
}
