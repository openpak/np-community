package xmpp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"openpak/np-common/coreclient"
	"openpak/np-common/fakecore"
	"openpak/np-common/identity"
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
	if !strings.Contains(got[host], base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xab}, 56))) || !strings.Contains(got[host], "NPEB01899_00") || !strings.Contains(got[host], "NPWR05636_00") {
		t.Fatalf("bridged presence: %q", got[host])
	}
	// the host's own console: its sign-in presence (mode=game, no data) must not hide the bridged game; a real hosting presence wins
	const signIn = `<x xmlns="` + nsStanza + `"></x><x xmlns="` + nsPresent + `"><mode xmlns="` + nsPresent + `">game</mode></x>`
	if over := s.bridgedXML(map[string]string{host: signIn}, []string{host}); over[host] != got[host] {
		t.Fatalf("a plain sign-in presence hid the bridged game: %q", over[host])
	}
	if own := s.bridgedXML(map[string]string{host: realHosting}, []string{host}); own[host] != realHosting {
		t.Fatalf("a real console's own hosting presence must win: %q", own[host])
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

// realHosting is what the owner's real PS3 sent while hosting Minecraft (2026-11-11 mc1.log: the stanza np-community relayed to a friend),
// as the relay stores it (children of <presence> without profile and status).
const realHostingStanza = `<presence from="PS3Test@pt.np.community.playstation.net/ps3" to="SwitchTest@gb.gb.np.playstation.net/ps3"><x xmlns="http://playstation.net/np/rt/profile"><name>PS3 Test User</name></x><status>Online on Pier</status><x xmlns="http://playstation.net/np/rt/stanzatype"></x><x xmlns="http://playstation.net/np/rt/ext" context="NPWR05636_00"><origin xmlns="http://playstation.net/np/rt/ext">NPWR05636_00</origin><data xmlns="http://playstation.net/np/rt/ext" inline="base64">UFMzVGVzdAAAAAAAAAAAAACAAAAAAAAAAAEAAQAAAAEAAQAAAAAIEgiEAAABDwAAAAAAAAAAAAA=</data></x><x xmlns="http://playstation.net/np/rt/presence"><mode xmlns="http://playstation.net/np/rt/presence">game</mode><objectId xmlns="http://playstation.net/np/rt/presence">NPEB01899_00</objectId><localName xmlns="http://playstation.net/np/rt/presence">Minecraft: PlayStation®3 Edition</localName><localLang xmlns="http://playstation.net/np/rt/presence">18</localLang><gameSpecified xmlns="http://playstation.net/np/rt/presence">Playing Alone - Enjoying the view!</gameSpecified></x></presence>`

var realHosting = func() string {
	n, e := nextNode(xml.NewDecoder(strings.NewReader(realHostingStanza)))
	if e != nil {
		panic(e)
	}
	return presenceData(n.Children)
}()

// Golden: the bridged presence for the real host's blob is byte for byte the presence the real PS3 sent while hosting.
func TestHostingChildrenEqualsRealPS3(t *testing.T) {
	blob, _ := base64.StdEncoding.DecodeString("UFMzVGVzdAAAAAAAAAAAAACAAAAAAAAAAAEAAQAAAAEAAQAAAAAIEgiEAAABDwAAAAAAAAAAAAA=")
	if got := hostingChildren("NPWR05636_00", blob, "", "", ""); got != realHosting {
		t.Fatalf("bridged presence differs from the real console's:\n got %s\nwant %s", got, realHosting)
	}
	if !wellFormed(hostingChildren("NPWR05636_00", blob, "A&B", "<n>", "x")) {
		t.Fatal("not escaped")
	}
	if got := blobsHex(mustChildren(t, realHostingStanza)); len(got) != 1 || got[0] != hex.EncodeToString(blob) {
		t.Fatalf("blob hex for the log: %v", got)
	}
}

func mustChildren(t *testing.T, stanza string) []node {
	n, e := nextNode(xml.NewDecoder(strings.NewReader(stanza)))
	if e != nil {
		t.Fatal(e)
	}
	return n.Children
}

// A PS3 viewer already online sees a bridged game at once (pushed on the PUT, not at the 30 s poll) and sees it end at once on DELETE; a viewer
// signing in later takes the same path (refresh).
func TestBridgedPresencePushedToOnlineViewer(t *testing.T) {
	t.Setenv("NP_BRIDGE_KEY", "bk")
	const key = "core-key-core-key-core-key-core-key"
	const host = "bbbbbbbb-0000-0000-0000-00000000000b"
	f := fakecore.New(key)
	f.AddAccount(fakecore.Account{ID: "a", Email: "a@example.org", Password: "pw", DisplayName: "Alice", Country: "US"})
	f.AddAccount(fakecore.Account{ID: host, DisplayName: "Hosty", Country: "GB"})
	f.AddFriend("a", host)
	core, e := coreclient.Dial("passthrough:///core", key, f.Serve(t)...)
	if e != nil {
		t.Fatal(e)
	}
	defer core.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	acct, _ := core.GetAccount(ctx, "a")
	id, err := identity.EnsureNPLink(ctx, core, acct)
	if err != nil {
		t.Fatal(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ticket.New(priv, nil)
	raw, _ := signer.Issue(ticket.Identity{AccountID: id.AccountID, LinkID: id.LinkID, OnlineID: id.OnlineID, Country: "us", Service: "IV0001-NPXS01001_00"}, time.Hour)
	s := &Server{Service: &community.Service{Core: core, Tickets: signer, Client: "pier", OS: "linux", Inbox: &community.MemoryInbox{}}, PollInterval: 30 * time.Second}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go s.Serve(ctx, ln)
	mux := http.NewServeMux()
	s.RegisterBridge(mux)
	call := func(method, body string) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(method, "/internal/bridge/presence/"+host, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer bk")
		mux.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", method, w.Code, w.Body)
		}
	}
	hostSession, _, e := core.RegisterPresence(ctx, host, "wiiu", "other", "", "")
	if e != nil {
		t.Fatal(e)
	}
	defer core.ExpirePresence(ctx, hostSession)

	conn, _ := net.Dial("tcp", ln.Addr().String())
	defer conn.Close()
	rd := bufio.NewReader(conn)
	d := xml.NewDecoder(rd)
	send := func(x string) { conn.Write([]byte(x)) }
	recv := func(pred func(node) bool) node {
		t.Helper()
		conn.SetReadDeadline(time.Now().Add(4 * time.Second))
		for {
			n, e := nextNode(d)
			if e != nil {
				t.Fatal("stream:", e)
			}
			if pred(n) {
				return n
			}
		}
	}
	open := `<stream:stream xmlns="jabber:client" xmlns:stream="http://etherx.jabber.org/streams" to="us.us.np.playstation.net" version="1.0">`
	send(open)
	streamStart(d)
	recv(func(n node) bool { return n.Name.Local == "features" })
	send(`<auth xmlns="` + saslNS + `" mechanism="I5R-TICKET-AUTH">` + base64.StdEncoding.EncodeToString(raw) + `</auth>`)
	recv(func(n node) bool { return n.Name.Local == "success" })
	d = xml.NewDecoder(rd)
	send(open)
	streamStart(d)
	recv(func(n node) bool { return n.Name.Local == "features" })
	send(`<iq type="set" id="bind"><bind xmlns="` + bindNS + `"><resource>ps3</resource></bind></iq><iq type="get" id="roster"><query xmlns="` + rosterNS + `"/></iq>`)
	fromHost := func(n node) bool {
		return n.Name.Local == "presence" && strings.HasPrefix(n.attr("from"), "Hosty@") && n.attr("type") == ""
	}
	if p := recv(fromHost); p.child(nsExt, "x") != nil {
		t.Fatalf("game shown before it was bridged: %+v", p)
	}
	time.Sleep(1500 * time.Millisecond) // past the stream's first poll, so only a push (not the 30 s poll) can deliver the game in time
	put := `{"game":"wiiu:8","title":"NPWR05636_00","data":"` + hex.EncodeToString(bytes.Repeat([]byte{7}, 56)) + `"}`
	call("PUT", put)
	p := recv(fromHost)
	game := p.child(nsPresent, "x")
	if x := p.child(nsExt, "x"); x == nil || game == nil || game.child(nsPresent, "mode") == nil || game.child(nsPresent, "mode").Text != "game" || game.child(nsPresent, "objectId").Text != "NPEB01899_00" {
		t.Fatalf("online viewer did not get the bridged game: %s", encoded(p))
	}
	call("DELETE", "")
	if p := recv(fromHost); p.child(nsExt, "x") != nil || p.child(nsPresent, "x") != nil {
		t.Fatalf("game still shown after it ended: %s", encoded(p))
	}
}

// A PS3 that joins a bridged game signals to the NpId in the owner's member entry: online id + <cc><cc>ps3, which the firmware turns into
// <online id>@<cc>.<cc>.np.playstation.net/ps3 (2026-11-13 15:49: PS3Test to SwitchTest@gb.gb.np.playstation.net/ps3). That is not the
// community JID the presence uses, and nobody holds it: the relay must still recognise the host by it, keep the stanza for the directory,
// and hold it (handled, no iq error back), or the joiner is told the host is unreachable and leaves.
func TestSignalingToABridgedHostIsHeldNotBounced(t *testing.T) {
	t.Setenv("NP_BRIDGE_KEY", "bk")
	const key = "core-key-core-key-core-key-core-key"
	const host = "aaaaaaaa-0000-0000-0000-00000000000b"
	f := fakecore.New(key)
	f.AddAccount(fakecore.Account{ID: host, DisplayName: "WiiUTest", Country: "GB"})
	core, e := coreclient.Dial("passthrough:///core", key, f.Serve(t)...)
	if e != nil {
		t.Fatal(e)
	}
	defer core.Close()
	s := &Server{Service: &community.Service{Core: core, Tickets: &ticket.Signer{}}, Log: slog.New(slog.NewTextHandler(nopWriter{}, nil))}
	mux := http.NewServeMux()
	s.RegisterBridge(mux)
	w := httptest.NewRecorder()
	r := httptest.NewRequest("PUT", "/internal/bridge/presence/"+host, strings.NewReader(`{"game":"wiiu:13","title":"NPWR05636_00","data":"`+strings.Repeat("ab", 56)+`"}`))
	r.Header.Set("Authorization", "Bearer bk")
	mux.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	acct, _ := core.GetAccount(context.Background(), host)
	link, _ := s.Service.EnsureLink(context.Background(), acct)
	stanza := func(to, kind string) node {
		return node{Name: xml.Name{Space: clientNS, Local: "iq"}, Attrs: []xml.Attr{{Name: xml.Name{Local: "type"}, Value: kind}, {Name: xml.Name{Local: "id"}, Value: "o1"}, {Name: xml.Name{Local: "to"}, Value: to}},
			Children: []node{{Name: xml.Name{Space: udpp2pNS, Local: "query"}, Text: "offer"}}}
	}
	joiner := &client{s: s, jid: "PS3Test@pt.np.np.playstation.net/ps3"}
	joiner.id.OnlineID, joiner.id.AccountID = "PS3Test", "acc-ps3"
	for _, to := range []string{link.OnlineID + "@gb.gb.np.playstation.net/ps3", link.OnlineID + "@gb.np.community.playstation.net", link.OnlineID + "@gb.np.community.playstation.net/ps3"} {
		for _, kind := range []string{"set", "result"} {
			if !joiner.forwardSignaling(stanza(to, kind)) {
				t.Fatalf("%s %s toward the bridged host was not held", kind, to)
			}
		}
	}
	if joiner.forwardSignaling(stanza("Somebody@gb.gb.np.playstation.net/ps3", "set")) {
		t.Fatal("a stanza toward nobody was held")
	}
	if n := len(s.bridge.signals); n != 6 {
		t.Fatalf("%d signals kept", n)
	}
}
