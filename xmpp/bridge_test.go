package xmpp

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openpak/np-common/coreclient"
	"openpak/np-common/fakecore"
	"openpak/np-common/ticket"
	"openpak/np-community/community"
)

// A game hosted on another console reaches PS3 friends through bridged presence: the 56-byte blob is stored with the host's own NP identity,
// shown only while the host's own console has no presence data, and withdrawn on DELETE. Signaling addressed to the host's JID (nobody holds it)
// is kept and logged in full for the directory; signaling to anybody else is not.
func TestBridgedPresenceAndSignaling(t *testing.T) {
	t.Setenv("NP_BRIDGE_KEY", "bk")
	const key = "core-key-core-key-core-key-core-key"
	const host = "aaaaaaaa-0000-0000-0000-00000000000a"
	f := fakecore.New(key)
	f.AddAccount(fakecore.Account{ID: host, DisplayName: "Hosty", Country: "GB"})
	core, e := coreclient.Dial("passthrough:///core", key, f.Serve(t)...)
	if e != nil {
		t.Fatal(e)
	}
	defer core.Close()
	s := &Server{Service: &community.Service{Core: core, Tickets: &ticket.Signer{}}, Log: slog.New(slog.NewTextHandler(nopWriter{}, nil))}
	mux := http.NewServeMux()
	s.RegisterBridge(mux)
	call := func(method, path, body, auth string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		mux.ServeHTTP(w, r)
		return w
	}
	blob := strings.Repeat("ab", 56)
	put := `{"game":"wiiu:5","title":"NPWR05636_00","data":"` + blob + `"}`
	if w := call("PUT", "/internal/bridge/presence/"+host, put, ""); w.Code != 401 {
		t.Fatalf("without a key: %d", w.Code)
	}
	if w := call("PUT", "/internal/bridge/presence/"+host, `{"data":"zz"}`, "Bearer bk"); w.Code != 400 {
		t.Fatalf("bad blob: %d", w.Code)
	}
	if w := call("PUT", "/internal/bridge/presence/"+host, `{"children":"<x><unclosed></x>"}`, "Bearer bk"); w.Code != 400 {
		t.Fatalf("malformed children: %d", w.Code)
	}
	if w := call("PUT", "/internal/bridge/presence/"+host, put, "Bearer bk"); w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	got := s.bridgedXML(nil, []string{host})
	if !strings.Contains(got[host], blob) || !strings.Contains(got[host], bridgeDataNS) || !strings.Contains(got[host], "NPWR05636_00") {
		t.Fatalf("bridged presence: %q", got[host])
	}
	if own := s.bridgedXML(map[string]string{host: "<own/>"}, []string{host}); own[host] != "<own/>" {
		t.Fatalf("a real console's own presence data must win: %q", own[host])
	}

	// signaling toward the host's JID: a connected-nobody address
	acct, _ := core.GetAccount(context.Background(), host)
	link, _ := s.Service.EnsureLink(context.Background(), acct)
	jid := community.JID(link.OnlineID, acct.Country)
	n := node{Name: xml.Name{Space: clientNS, Local: "iq"}, Attrs: []xml.Attr{{Name: xml.Name{Local: "type"}, Value: "set"}, {Name: xml.Name{Local: "id"}, Value: "o1"}, {Name: xml.Name{Local: "to"}, Value: jid}},
		Children: []node{{Name: xml.Name{Space: udpp2pNS, Local: "query"}, Text: "offer"}}}
	joiner := &client{jid: "Joiner@us.np.community.playstation.net/ps3"}
	joiner.id.OnlineID, joiner.id.AccountID = "Joiner", "acc-joiner"
	s.recordSignal(joiner, n, jid)
	s.recordSignal(joiner, n, "Somebody@us.np.community.playstation.net")
	w := call("GET", "/internal/bridge/signaling?after=0", "", "Bearer bk")
	var evs []signalEvent
	if err := json.Unmarshal(w.Body.Bytes(), &evs); err != nil || len(evs) != 1 {
		t.Fatalf("signaling feed: %s", w.Body)
	}
	e1 := evs[0]
	if e1.Game != "wiiu:5" || e1.Kind != "signal" || e1.Native != "Joiner" || e1.Account != "acc-joiner" || e1.Data["to"] != jid || !strings.Contains(e1.Data["stanza"], "offer") || !strings.Contains(e1.Data["stanza"], `id="o1"`) {
		t.Fatalf("event %+v", e1)
	}
	if w := call("GET", "/internal/bridge/signaling?after=1", "", "Bearer bk"); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("cursor: %s", w.Body)
	}

	if w := call("DELETE", "/internal/bridge/presence/"+host, "", "Bearer bk"); w.Code != 200 || len(s.bridgedXML(nil, []string{host})) != 0 {
		t.Fatalf("delete: %d", w.Code)
	}
}

type nopWriter struct{}

func (nopWriter) Write(p []byte) (int, error) { return len(p), nil }
