package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

// parsePassphrases reads "COMMID=hex256[,COMMID=hex256...]".
func parsePassphrases(v string) (map[string][]byte, error) {
	out := map[string][]byte{}
	for _, e := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == '\n' || r == ';' }) {
		id, h, ok := strings.Cut(strings.TrimSpace(e), "=")
		b, err := hex.DecodeString(strings.TrimSpace(h))
		if !ok || err != nil || len(b) != 128 {
			return nil, fmt.Errorf("entry %q: want COMMID=<256 hex chars>", id)
		}
		out[id] = b
	}
	return out, nil
}

const hdrLen = 32

// mac is HMAC-SHA1(key = first 16 bytes of the passphrase, packet with the MAC field zeroed)[:16]
// (matching2-protocol.md section 4; verified against a real 0x1001 request from firmware 4.93).
func mac(pass []byte, pkt []byte) []byte {
	z := append([]byte(nil), pkt...)
	for i := 16; i < 32 && i < len(z); i++ {
		z[i] = 0
	}
	h := hmac.New(sha1.New, pass[:16])
	h.Write(z)
	return h.Sum(nil)[:16]
}

func tlv(id, typ uint16, val []byte) []byte {
	b := make([]byte, 4, 4+len(val))
	binary.BigEndian.PutUint16(b, typ<<12|id)
	binary.BigEndian.PutUint16(b[2:], uint16(len(val)))
	return append(b, val...)
}

// reply builds a reply frame to the request header req (first 12 bytes echo the id).
func reply(pass []byte, cmd uint16, result byte, reqID []byte, elems ...[]byte) []byte {
	b := make([]byte, hdrLen)
	b[0], b[1] = 0x11, 2
	binary.BigEndian.PutUint16(b[4:], cmd)
	b[6] = result
	copy(b[8:12], reqID)
	for _, e := range elems {
		b = append(b, e...)
	}
	binary.BigEndian.PutUint16(b[2:], uint16(len(b)))
	copy(b[16:32], mac(pass, b))
	return b
}

// elements walks the TLVs of a frame.
func elements(pkt []byte) map[uint16][]byte {
	out := map[uint16][]byte{}
	for p := pkt[hdrLen:]; len(p) >= 4; {
		id := binary.BigEndian.Uint16(p) & 0xfff
		n := int(binary.BigEndian.Uint16(p[2:]))
		if 4+n > len(p) {
			break
		}
		out[id] = p[4 : 4+n]
		p = p[4+n:]
	}
	return out
}

// connState is what one agent connection has negotiated: the session MAC key the 0x1002 reply handed out.
type connState struct{ key []byte }

func macKey(key, pkt []byte) []byte {
	z := append([]byte(nil), pkt...)
	for i := 16; i < 32 && i < len(z); i++ {
		z[i] = 0
	}
	h := hmac.New(sha1.New, key)
	h.Write(z)
	return h.Sum(nil)[:16]
}

// answer returns the reply frames for the requests in one received chunk. Commands it does not
// implement are recorded by the caller and get no reply.
func (r *recorder) answer(chunk []byte, st *connState, note func(string, ...any)) [][]byte {
	if len(r.pass) == 0 {
		return nil
	}
	var out [][]byte
	for b := chunk; len(b) >= 4; {
		if b[0] == 0 {
			b = b[1:]
			continue
		}
		n := int(binary.BigEndian.Uint16(b[2:4]))
		if b[0] != 0x11 || n < hdrLen || n > len(b) {
			return out
		}
		pkt := b[:n]
		b = b[n:]
		if pkt[1] != 1 {
			continue
		}
		cmd := binary.BigEndian.Uint16(pkt[4:6])
		el := elements(pkt)
		commID := string(el[0x10])
		pass, ok := r.pass[commID]
		if !ok {
			note("request 0x%04x: no passphrase for %q, not answering", cmd, commID)
			continue
		}
		if cmd == 0x1002 {
			// Login: the first request on the TLS connection, never MAC'd by the client. It carries the
			// whole title passphrase (element 0x15); the reply hands out the session MAC key (0x22), two
			// opaque tokens (0x23, 0x5d) that later requests echo, a u32 (0x14) and a u16 (0x74).
			if string(el[0x15]) != string(pass) {
				note("request 0x1002: passphrase in the login does not match the configured one")
				continue
			}
			st.key = make([]byte, 20)
			rand.Read(st.key)
			tok := func() []byte { b := make([]byte, 16); rand.Read(b); return b }
			note("request 0x1002: login accepted, session key issued")
			out = append(out, reply(pass, cmd, 0, pkt[8:12],
				tlv(0x14, 5, []byte{0, 0, 0, 1}), tlv(0x22, 2, st.key), tlv(0x23, 2, tok()), tlv(0x5d, 2, tok()), tlv(0x74, 4, []byte{0, 1})))
			continue
		}
		if cmd != 0x1001 {
			if st.key == nil {
				note("request 0x%04x: no session yet", cmd)
			} else if string(macKey(st.key, pkt)) == string(pkt[16:32]) {
				note("request 0x%04x: session MAC ok", cmd)
			} else {
				note("request 0x%04x: session MAC mismatch", cmd)
			}
			continue
		}
		if string(mac(pass, pkt)) != string(pkt[16:32]) {
			note("request 0x%04x: MAC mismatch", cmd)
			continue
		}
		note("request 0x%04x: MAC ok title=%s", cmd, commID)
		switch cmd {
		case 0x1001:
			// element 0x1d = {u16, u8}: stored in the agent context (+10, +0xc) and returned to the caller.
			out = append(out, reply(pass, cmd, 0, pkt[8:12], tlv(0x1d, 0, []byte{0, 1, 0})))
		}
	}
	return out
}
