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

func TestMatchingAgentAdvertised(t *testing.T) {
	s := &Service{ResourceKey: bytes.Repeat([]byte{0x42}, 20), MatchingAgent: "matching.np.community.playstation.net:3480"}
	mux := http.NewServeMux()
	s.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/np/resource/title/NPWR04270_00/matching/NPWR04270_00-matching.xml", nil))
	b := w.Body.String()
	want := `<matching2 status="alive" titleid="NPWR04270_00"><agent-fqdn id="1" status="alive" port="3480">matching.np.community.playstation.net</agent-fqdn></matching2>`
	if !strings.Contains(b, want) {
		t.Fatalf("got %q", b)
	}
	for _, bad := range []string{"", "nohost", ":3480", "h:99999", "h:x"} {
		if _, _, ok := parseMatchingAgent(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestMatchingKeyOwnSlot(t *testing.T) {
	tr, mk := bytes.Repeat([]byte{0x42}, 20), bytes.Repeat([]byte{0x17}, 20)
	get := func(s *Service) string {
		mux := http.NewServeMux()
		s.Register(mux)
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/np/resource/title/NPWR04270_00/matching/NPWR04270_00-matching.xml", nil))
		return w.Body.String()
	}
	a := get(&Service{ResourceKey: tr, MatchingKey: mk})
	b := get(&Service{ResourceKey: tr})
	c := get(&Service{MatchingKey: mk})
	if a == b {
		t.Fatal("matching resource ignored its own key")
	}
	if a != c {
		t.Fatal("matching key alone must be enough")
	}
}

func TestMatchingAgentOnlyForListedTitles(t *testing.T) {
	s := &Service{ResourceKey: bytes.Repeat([]byte{0x42}, 20), MatchingAgent: "h.example:3480", MatchingTitles: []string{"NPWR04270_00"}}
	mux := http.NewServeMux()
	s.Register(mux)
	get := func(id string) string {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", "/np/resource/title/"+id+"/matching/"+id+"-matching.xml", nil))
		return w.Body.String()
	}
	if !strings.Contains(get("NPWR04270_00"), `status="alive"`) {
		t.Fatal("listed title must see the agent")
	}
	if !strings.Contains(get("NPWR01234_00"), `status="maintenance"`) {
		t.Fatal("other titles stay in maintenance")
	}
}

// Minecraft: PlayStation 3 Edition (NP communication id NPWR05636_00) is answered next to Terraria once it is listed in NP_MATCHING_AGENT_TITLES.
func TestMatchingAgentForMinecraftPS3(t *testing.T) {
	s := &Service{ResourceKey: bytes.Repeat([]byte{0x42}, 20), MatchingAgent: "h.example:3480", MatchingTitles: []string{"NPWR04270_00", "NPWR05636_00"}}
	mux := http.NewServeMux()
	s.Register(mux)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/np/resource/title/NPWR05636_00/matching/NPWR05636_00-matching.xml", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `<matching2 status="alive" titleid="NPWR05636_00"><agent-fqdn id="1" status="alive" port="3480">h.example</agent-fqdn></matching2>`) {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
}
