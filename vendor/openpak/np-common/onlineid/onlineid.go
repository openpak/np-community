// Package onlineid holds the online-ID rules and the user_id derivation.
package onlineid

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"strings"
)

const (
	MinLen = 3
	MaxLen = 16
)

// Valid reports whether s is an acceptable online ID: 3 to 16 characters,
// ASCII letters, digits, '-' and '_', starting with a letter. UNCONFIRMED
// against the real service; these are the rules np-account enforces.
func Valid(s string) bool {
	if len(s) < MinLen || len(s) > MaxLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		letter := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
		switch {
		case letter:
		case i == 0:
			return false
		case (c >= '0' && c <= '9') || c == '-' || c == '_':
		default:
			return false
		}
	}
	return true
}

// FromDisplayName turns a free-form display name into a candidate online ID:
// disallowed characters become '_', a leading non-letter gets a 'P' prefix,
// the result is cut to 16 and padded to 3. ok is false when nothing usable
// is left (an empty name).
func FromDisplayName(name string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r < 128 && ((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_'):
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" {
		return "", false
	}
	if c := s[0]; !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
		s = "P" + s
	}
	if len(s) > MaxLen {
		s = s[:MaxLen]
	}
	for len(s) < MinLen {
		s += "_"
	}
	return s, Valid(s)
}

// UserID derives the stable 64-bit user_id claim from a core link id: the
// first 8 bytes of SHA-256("openpak-np-user-id-v1" 0x00 link_id), big
// endian, with the top bit cleared so it is a positive int64. It is stable
// for the life of the link, differs per link, and needs no storage. It is
// returned as a decimal string, the form the id_token carries.
func UserID(linkID string) string {
	sum := sha256.Sum256([]byte("openpak-np-user-id-v1\x00" + linkID))
	return strconv.FormatUint(binary.BigEndian.Uint64(sum[:8])&^(1<<63), 10)
}

// AccountID is the native NP account number. Keep ticket and bindAccount IDs identical.
func AccountID(accountID string) uint64 {
	sum := sha256.Sum256([]byte("openpak-np-native-account-id-v1\x00" + accountID))
	return binary.BigEndian.Uint64(sum[:8])
}
