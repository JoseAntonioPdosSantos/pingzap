package phone

import (
	"slices"
	"testing"
)

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"67999999999":         "5567999999999",
		"+55 (11) 98888-7777": "5511988887777",
		"6733334444":          "556733334444",
		"":                    "",
		"abc":                 "",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVariants(t *testing.T) {
	cases := map[string][]string{
		"67999999999":         {"5567999999999", "556799999999"}, // with and without the ninth digit
		"+55 (11) 98888-7777": {"5511988887777", "551188887777"},
		"6733334444":          {"556733334444", "5567933334444"},
		"14155550123":         {"5514155550123"}, // 11 digits are taken as area code + phone
		"":                    nil,
	}
	for in, want := range cases {
		if got := Variants(in); !slices.Equal(got, want) {
			t.Errorf("Variants(%q) = %v, want %v", in, got, want)
		}
	}
}
