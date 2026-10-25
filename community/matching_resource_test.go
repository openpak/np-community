package community

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestNativeMatchingDiscovery(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 20)
	s := &Service{ResourceKey: key}
	mux := http.NewServeMux()
	s.Register(mux)
	path := "/np/resource/title/NPWR04270_00/matching/NPWR04270_00-matching.xml"
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 200 {
		t.Fatalf("status %d", w.Code)
	}
	body := w.Body.Bytes()
	if len(body) < 66 || !strings.HasPrefix(string(body), "<!--Sce-Np-Signature: ") {
		t.Fatal("missing native signature frame")
	}
	// Independently verify the firmware's signed-resource framing/padding.
	signature, err := hex.DecodeString(string(body[22:62]))
	if err != nil {
		t.Fatal(err)
	}
	payload := body[66:]
	mac := hmac.New(sha1.New, key)
	mac.Write(payload)
	mac.Write(make([]byte, (64-len(payload)%64)%64))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		t.Fatal("invalid native resource MAC")
	}
	var doc struct {
		Name   xml.Name `xml:"matching2"`
		Status string   `xml:"status,attr"`
		Title  string   `xml:"titleid,attr"`
		Agents []string `xml:"agent-fqdn"`
	}
	if err := xml.Unmarshal(payload, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Status != "maintenance" || doc.Title != "NPWR04270_00" || len(doc.Agents) != 0 {
		t.Fatalf("unsupported native agent advertised: %+v", doc)
	}
	if w.Header().Get("Content-Length") != strconv.Itoa(len(body)) {
		t.Fatal("bad content length")
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("maintenance response cached")
	}
	for _, bad := range []string{"/np/resource/title/invalid/matching/invalid-matching.xml", "/np/resource/title/NPWR04270_00/matching/NPWR99999_00-matching.xml"} {
		w = httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", bad, nil))
		if w.Code != 404 {
			t.Fatalf("accepted invalid discovery path %s", bad)
		}
	}
	s.ResourceKey = nil
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
	if w.Code != 503 {
		t.Fatal("served unsigned resource")
	}
}
