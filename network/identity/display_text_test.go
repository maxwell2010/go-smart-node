package identity

import (
	"strings"
	"testing"
)

func TestValidDisplayName(t *testing.T) {
	for _, name := range []string{"Валидатор №1", "عقدة ١", "Cafe\u0301", "👩‍💻", strings.Repeat("я", 35)} {
		if !ValidDisplayName(name, 70) {
			t.Errorf("should allow %q", name)
		}
	}
	for _, name := range []string{"", " \u00a0\u3000", "\u200b", "\u200d", "\u0301", "\u2800", "\u3164",
		"bad\u0085name", "bad\u202ename", "bad\u2028name", "bad\ufffdname", "bad\xffname", "bad\nname", strings.Repeat("я", 36)} {
		if ValidDisplayName(name, 70) {
			t.Errorf("should reject %q", name)
		}
	}
	if !ValidPublicText("", 280) {
		t.Error("optional text must allow an empty value")
	}
}
