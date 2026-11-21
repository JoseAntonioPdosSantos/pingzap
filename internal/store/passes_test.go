package store

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestScheduleAllows(t *testing.T) {
	loc := time.UTC
	at := func(day int, hm string) time.Time {
		// 2026-11-15 is a Sunday.
		h, _ := minutes(hm)
		return time.Date(2026, 11, 15+day, h/60, h%60, 0, 0, loc)
	}
	work := Schedule{Windows: []Window{{Days: []int{1, 2, 3, 4, 5}, Start: "08:00", End: "18:00"}}}
	night := Schedule{Windows: []Window{{Days: []int{5}, Start: "22:00", End: "06:00"}}}
	cases := []struct {
		name string
		sc   Schedule
		t    time.Time
		want bool
	}{
		{"empty is always", Schedule{}, at(0, "03:00"), true},
		{"weekday inside", work, at(1, "08:00"), true},
		{"weekday end is out", work, at(1, "18:00"), false},
		{"weekday before", work, at(3, "07:59"), false},
		{"sunday", work, at(0, "10:00"), false},
		{"night, friday evening", night, at(5, "23:30"), true},
		{"night, saturday early morning", night, at(6, "05:59"), true},
		{"night, saturday 06:00", night, at(6, "06:00"), false},
		{"night, saturday evening", night, at(6, "23:30"), false},
		{"night, friday early morning", night, at(5, "03:00"), false},
	}
	for _, c := range cases {
		if got := c.sc.Allows(c.t); got != c.want {
			t.Errorf("%s: Allows(%v) = %v, want %v", c.name, c.t, got, c.want)
		}
	}

	next, ok := work.Next(at(5, "19:00")) // Friday evening → Monday
	if !ok || !next.Equal(at(8, "08:00")) {
		t.Errorf("Next = %v, %v; want Monday 08:00", next, ok)
	}
}

func TestScheduleValidate(t *testing.T) {
	bad := []Window{
		{Days: []int{1}, Start: "8:00", End: "18:00"},
		{Days: []int{1}, Start: "08:00", End: "25:00"},
		{Days: []int{1}, Start: "08:00", End: "08:00"},
		{Days: nil, Start: "08:00", End: "18:00"},
		{Days: []int{7}, Start: "08:00", End: "18:00"},
	}
	for _, w := range bad {
		sc := Schedule{Windows: []Window{w}}
		var ve *ValidationError
		if err := sc.Validate(); !errors.As(err, &ve) {
			t.Errorf("%+v: err = %v, want a validation error", w, err)
		}
	}
	sc := Schedule{Windows: []Window{{Days: []int{3, 1, 3}, Start: "00:00", End: "24:00"}}}
	if err := sc.Validate(); err != nil || len(sc.Windows[0].Days) != 2 || sc.Windows[0].Days[0] != 1 {
		t.Errorf("valid window: %+v, %v", sc, err)
	}
}

func TestPassLifecycle(t *testing.T) {
	s := open(t)
	dev, _ := s.CreateDevice(Device{Name: "Portão", ClientID: "esp-1", CmdTopic: "a/cmd", DataTopic: "a/data", StatusTopic: "a/status"})
	raw, _ := json.Marshal(map[string]any{})
	b, err := s.SaveBot(Bot{Name: "Portão", Kind: KindFlow, Enabled: true, DeviceID: &dev.ID, Config: raw})
	if err != nil {
		t.Fatal(err)
	}

	var ve *ValidationError
	if _, err := s.CreatePass(b.ID, PassInput{Name: " "}); !errors.As(err, &ve) {
		t.Fatalf("empty name: %v", err)
	}
	soon := time.Now().Add(time.Hour)
	if _, err := s.CreatePass(b.ID, PassInput{Name: "x", StartsAt: &soon, ExpiresAt: &soon}); !errors.As(err, &ve) {
		t.Fatalf("expires not after start: %v", err)
	}
	if _, err := s.CreatePass(b.ID, PassInput{Name: "x", Timezone: "Marte/Olimpo"}); !errors.As(err, &ve) {
		t.Fatalf("bad zone: %v", err)
	}

	p, err := s.CreatePass(b.ID, PassInput{Name: "Visita", MaxUses: 2, ExpiresAt: &soon, Timezone: "America/Manaus"})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Token) < 32 || p.UsesLeft() != 2 {
		t.Fatalf("pass = %+v", p)
	}
	if got, err := s.PassByToken(p.Token); err != nil || got.ID != p.ID {
		t.Fatalf("PassByToken = %+v, %v", got, err)
	}

	// Exactly MaxUses claims succeed, and a released one can be claimed again.
	for i := 0; i < 2; i++ {
		if ok, err := s.ClaimPassUse(p.ID); !ok || err != nil {
			t.Fatalf("claim %d: %v, %v", i, ok, err)
		}
	}
	if ok, _ := s.ClaimPassUse(p.ID); ok {
		t.Fatal("claimed past the limit")
	}
	s.ReleasePassUse(p.ID)
	if ok, _ := s.ClaimPassUse(p.ID); !ok {
		t.Fatal("could not claim a released use")
	}
	got, _ := s.pass(b.ID, p.ID)
	if st, _ := got.Status(time.Now()); st != PassUsedUp {
		t.Fatalf("status = %q, want used_up", st)
	}
	if st, _ := got.Status(soon.Add(time.Minute)); st != PassExpired {
		t.Fatalf("status after expiry = %q, want expired", st)
	}

	// Renewing starts the count again.
	got, err = s.UpdatePass(b.ID, p.ID, PassInput{Name: "Visita", MaxUses: 2, ExpiresAt: &soon, Timezone: "America/Manaus", ResetUses: true})
	if err != nil || got.Uses != 0 || got.Token != p.Token {
		t.Fatalf("renew: %+v, %v", got, err)
	}

	if err := s.DeletePass(b.ID, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PassByToken(p.Token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted pass: %v", err)
	}
}

func TestPassStatusSchedule(t *testing.T) {
	p := RemotePass{Timezone: "America/Manaus", Schedule: Schedule{Windows: []Window{{Days: []int{1}, Start: "09:00", End: "10:00"}}}}
	// Monday 2026-11-16 09:30 in Manaus is 13:30 UTC.
	in := time.Date(2026, 11, 16, 13, 30, 0, 0, time.UTC)
	if st, _ := p.Status(in); st != PassActive {
		t.Fatalf("inside the window: %q", st)
	}
	out := time.Date(2026, 11, 16, 9, 30, 0, 0, time.UTC) // 05:30 in Manaus
	st, msg := p.Status(out)
	if st != PassOutside || msg == "" {
		t.Fatalf("outside the window: %q %q", st, msg)
	}
}
