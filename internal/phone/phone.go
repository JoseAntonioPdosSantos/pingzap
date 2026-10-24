// Package phone normalizes Brazilian phone numbers to the form WhatsApp uses.
package phone

import (
	"strings"
	"unicode"
)

const countryCode = "55"

// Normalize returns the number as digits only, with the country code. Area
// code + phone (10 or 11 digits) gets Brazil's code added; anything else is
// kept as typed. It returns "" when the input has no digits.
func Normalize(number string) string {
	user := digits(number)
	if len(user) == 10 || len(user) == 11 {
		user = countryCode + user
	}
	return user
}

// Variants returns the forms a number can take on WhatsApp: older Brazilian
// accounts are identified without the ninth digit, so both forms are listed.
func Variants(number string) []string {
	user := Normalize(number)
	if user == "" {
		return nil
	}
	variants := []string{user}
	if !strings.HasPrefix(user, countryCode) {
		return variants
	}
	switch {
	case len(user) == 13 && user[4] == '9':
		variants = append(variants, user[:4]+user[5:])
	case len(user) == 12:
		variants = append(variants, user[:4]+"9"+user[4:])
	}
	return variants
}

func digits(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}
