package types

import (
	"errors"
	"net/url"
	"unicode/utf8"
)

const MaxValidatorAvatarURIBytes = 512

// ValidateValidatorAvatarURI checks a stored public reference only. It performs
// no fetch, image decode or proof of content availability. Empty clears a URI
// through self-control; genesis records require a nonempty reference separately.
func ValidateValidatorAvatarURI(value string) error {
	if len(value) > MaxValidatorAvatarURIBytes || !utf8.ValidString(value) {
		return errors.New("validator avatar URI requires bounded valid UTF-8")
	}
	if value == "" {
		return nil
	}
	for _, r := range value {
		if r < 32 || r == 127 {
			return errors.New("validator avatar URI contains control characters")
		}
	}
	u, err := url.Parse(value)
	if err != nil || u.User != nil || u.Fragment != "" || u.Host == "" || (u.Scheme != "https" && u.Scheme != "ipfs") {
		return errors.New("avatar URI requires bounded HTTPS or IPFS reference")
	}
	return nil
}
