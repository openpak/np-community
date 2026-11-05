package main

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestAnnotate(t *testing.T) {
	// keepalive, then a request frame cmd 0x1001 len 8, then a truncated one
	b := []byte{0x00, 0x11, 0x01, 0x00, 0x08, 0x10, 0x01, 0x00, 0x00, 0x11, 0x01, 0x00, 0x20, 0x10, 0x02}
	got := strings.Join(annotate(b), "|")
	want := "keepalive 0x00|frame request len=8 cmd=0x1001|frame request len=32 cmd=0x1002 (truncated: have 6)"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if g := annotate([]byte{0x16, 0x03, 0x01, 0x00}); !strings.HasPrefix(g[0], "not a frame") {
		t.Fatal(g)
	}
	if g := annotate([]byte{0x11, 1}); !strings.HasPrefix(g[0], "short header") {
		t.Fatal(g)
	}
}

func TestDumpChunk(t *testing.T) {
	d := dumpChunk("T", []byte{0x00, 'A'})
	if !strings.Contains(d, "T recv 2 bytes") || !strings.Contains(d, "00000000  00 41") {
		t.Fatal(d)
	}
}

func TestRedactSecrets(t *testing.T) {
	pkt := make([]byte, hdrLen)
	pkt[0], pkt[1] = 0x11, 1
	pkt = append(pkt, tlv(0x11, 2, []byte("ticket-secret"))...)
	pkt = append(pkt, tlv(0x73, 4, []byte{0, 1})...)
	binary.BigEndian.PutUint16(pkt[2:], uint16(len(pkt)))
	out := redact(pkt)
	if bytes.Contains(out, []byte("ticket-secret")) || !bytes.Contains(pkt, []byte("ticket-secret")) {
		t.Fatal("redaction failed or mutated the input")
	}
}
