package main

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"strings"
)

var kinds = map[byte]string{1: "request", 2: "reply", 4: "event"}

// annotate describes the Matching2 frames found in one received chunk
// (0x00 keepalive, or 0x11/kind/len16/cmd16 ...), one line per frame. It
// only reads; anything that does not parse is reported, never guessed at.
func annotate(b []byte) []string {
	var out []string
	for len(b) > 0 {
		if b[0] == 0x00 {
			out = append(out, "keepalive 0x00")
			b = b[1:]
			continue
		}
		if len(b) < 4 {
			out = append(out, fmt.Sprintf("short header (%d bytes)", len(b)))
			return out
		}
		n := int(binary.BigEndian.Uint16(b[2:4]))
		k, ok := kinds[b[1]]
		if !ok {
			k = fmt.Sprintf("kind=%d", b[1])
		}
		if b[0] != 0x11 || n < 4 {
			out = append(out, fmt.Sprintf("not a frame: first=0x%02x kind=%s len=%d", b[0], k, n))
			return out
		}
		line := fmt.Sprintf("frame %s len=%d", k, n)
		if len(b) >= 6 {
			line += fmt.Sprintf(" cmd=0x%04x", binary.BigEndian.Uint16(b[4:6]))
		}
		if n > len(b) {
			line += fmt.Sprintf(" (truncated: have %d)", len(b))
			out = append(out, line)
			return out
		}
		out = append(out, line)
		b = b[n:]
	}
	return out
}

// dumpChunk renders one received chunk as a timestamped hexdump plus frame notes.
func dumpChunk(ts string, b []byte) string {
	var s strings.Builder
	fmt.Fprintf(&s, "%s recv %d bytes\n", ts, len(b))
	for _, l := range annotate(b) {
		fmt.Fprintf(&s, "  # %s\n", l)
	}
	s.WriteString(hex.Dump(b))
	return s.String()
}

// dumpSent renders one chunk the recorder sent.
func dumpSent(ts string, b []byte) string {
	var s strings.Builder
	fmt.Fprintf(&s, "%s sent %d bytes\n", ts, len(b))
	for _, l := range annotate(b) {
		fmt.Fprintf(&s, "  # %s\n", l)
	}
	s.WriteString(hex.Dump(b))
	return s.String()
}
