package identity

import (
	"unicode"
	"unicode/utf8"
)

// ValidPublicText checks byte length and permits ordinary Unicode text,
// including joiners used by some scripts and emoji.
func ValidPublicText(value string, maximum int) bool {
	if len(value) > maximum || !utf8.ValidString(value) {
		return false
	}
	for _, ch := range value {
		if ch == utf8.RuneError || unicode.IsControl(ch) || unicode.Is(unicode.Zl, ch) || unicode.Is(unicode.Zp, ch) ||
			(unicode.Is(unicode.Cf, ch) && ch != '\u200c' && ch != '\u200d') {
			return false
		}
	}
	return true
}

// ValidDisplayName also requires a visible character beyond whitespace,
// joiners and combining marks.
func ValidDisplayName(value string, maximum int) bool {
	if !ValidPublicText(value, maximum) {
		return false
	}
	for _, ch := range value {
		if ch == '\u2800' || unicode.Is(unicode.Other_Default_Ignorable_Code_Point, ch) {
			continue
		}
		if unicode.IsLetter(ch) || unicode.IsNumber(ch) || unicode.IsPunct(ch) || unicode.IsSymbol(ch) {
			return true
		}
	}
	return false
}
