package whatsapp

import "testing"

func TestTriggerUsers(t *testing.T) {
	users := triggerUsers([]string{"67999999999", "+55 (11) 98888-7777", "6733334444", ""})

	for _, user := range []string{
		"5567999999999", "556799999999", // with and without the ninth digit
		"5511988887777", "551188887777",
		"556733334444", "5567933334444",
	} {
		if _, ok := users[user]; !ok {
			t.Errorf("expected %s among the users: %v", user, users)
		}
	}
	if got := users["556799999999"]; got != "67999999999" {
		t.Errorf("configured number = %q, want 67999999999", got)
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
