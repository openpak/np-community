package main

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// The request is a real 0x1001 captured from Terraria (BLES01938) on firmware 4.93 in Pier.
func TestRealRequest(t *testing.T) {
	pkt, _ := hex.DecodeString("1101003610010000101000010000000006" + "7c6091c2d77a8fee815d95641ed1bd" + "1010000c4e50575230343237305f3030" + "40730002" + "0001")
	pass := bytes.Repeat([]byte{0}, 128)
	copy(pass, mustHex(t, "0b6476018b974b55ff338d8ba299c62492963927f465d5c3b0ab06be0beca59e"))
	if !bytes.Equal(mac(pass, pkt), pkt[16:32]) {
		t.Fatal("MAC of the captured request does not verify")
	}
	r := &recorder{pass: map[string][]byte{"NPWR04270_00": pass}}
	out := r.answer(pkt, func(string, ...any) {})
	if len(out) != 1 || out[0][1] != 2 || out[0][4] != 0x10 || out[0][5] != 1 {
		t.Fatalf("reply: %x", out)
	}
	if !bytes.Equal(out[0][8:12], pkt[8:12]) || !bytes.Equal(mac(pass, out[0]), out[0][16:32]) {
		t.Fatal("reply id or MAC")
	}
	if len(out[0]) != 32+4+3 {
		t.Fatalf("len %d", len(out[0]))
	}
}

func mustHex(t *testing.T, s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
