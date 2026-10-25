package ticket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"strings"
)

// Avatar capabilities are short enough for VSH's 127-byte URL field. They expose
// only an active account's public avatar, never credentials or profile secrets.
func (s *Signer) AvatarCapability(account string) string {
	if len(account) == 0 || len(account) > 36 {
		return ""
	}
	raw := append([]byte(account), make([]byte, 4)...)
	// Stable during an hour, valid for at most 25 hours.
	binary.BigEndian.PutUint32(raw[len(account):], uint32((s.now().Unix()/3600+25)*3600))
	return base64.RawURLEncoding.EncodeToString(append(raw, s.avatarMAC(raw)...))
}
func (s *Signer) avatarMAC(raw []byte) []byte {
	key := sha256.Sum256(append(append([]byte{}, s.key.Seed()...), []byte("openpak-avatar-v1")...))
	m := hmac.New(sha256.New, key[:])
	m.Write(raw)
	return m.Sum(nil)[:16]
}
func (s *Signer) VerifyAvatar(cap string) (string, error) {
	raw, e := base64.RawURLEncoding.DecodeString(cap)
	if e != nil || len(raw) < 21 || len(raw) > 56 {
		return "", ErrInvalid
	}
	payload, sig := raw[:len(raw)-16], raw[len(raw)-16:]
	if !hmac.Equal(sig, s.avatarMAC(payload)) {
		return "", ErrInvalid
	}
	account := string(payload[:len(payload)-4])
	expires := int64(binary.BigEndian.Uint32(payload[len(payload)-4:]))
	if account == "" || strings.ContainsAny(account, "/\x00") || expires <= s.now().Unix() || expires > s.now().Unix()+25*3600 {
		return "", ErrInvalid
	}
	return account, nil
}
