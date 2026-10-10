package whatsapp

import (
	"strings"
	"unicode"
)

const countryCode = "55"

// triggerUsers returns, for each configured number, the forms it can take on
// WhatsApp: with the country code and, for Brazilian accounts, with and
// without the ninth digit. The map value is the number as it was configured.
func triggerUsers(numbers []string) map[string]string {
	users := make(map[string]string)
	for _, number := range numbers {
		user := digits(number)
		if user == "" {
			continue
		}
		// Area code + phone, without the country code.
		if len(user) == 10 || len(user) == 11 {
			user = countryCode + user
		}
		users[user] = number

		if !strings.HasPrefix(user, countryCode) {
			continue
		}
		switch {
		case len(user) == 13 && user[4] == '9':
			users[user[:4]+user[5:]] = number
		case len(user) == 12:
			users[user[:4]+"9"+user[4:]] = number
		}
	}
	return users
}

func digits(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsDigit(r) {
			return r
		}
		return -1
	}, s)
}

// matchesKeyword reports whether the text contains the keyword, ignoring
// case. An empty keyword accepts any message, including those without text
// (audio, sticker, photo without a caption).
func matchesKeyword(text, keyword string) bool {
	if keyword == "" {
		return true
	}
	return strings.Contains(strings.ToLower(text), strings.ToLower(keyword))
}
