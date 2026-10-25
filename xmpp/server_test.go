package xmpp

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/xml"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"

	"openpak/np-common/coreclient"
	"openpak/np-common/fakecore"
	"openpak/np-common/identity"
	"openpak/np-common/ticket"
	"openpak/np-community/community"
)

func TestCoreFriendsPresenceAndDisconnect(t *testing.T) {
	const key = "core-key-core-key-core-key-core-key"
	f := fakecore.New(key)
	f.AddAccount(fakecore.Account{ID: "a", Email: "a@example.org", Password: "pw", DisplayName: "Alice", Country: "US"})
	f.AddAccount(fakecore.Account{ID: "b", DisplayName: "Bob", Country: "GB"})
	f.AddFriend("a", "b")
	core, e := coreclient.Dial("passthrough:///core", key, f.Serve(t)...)
	if e != nil {
		t.Fatal(e)
	}
	defer core.Close()
	acct, e := core.GetAccount(context.Background(), "a")
	if e != nil {
		t.Fatal(e)
	}
	id, err := identity.EnsureNPLink(context.Background(), core, acct)
	if err != nil {
		t.Fatal(err)
	}
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ticket.New(priv, nil)
	raw, e := signer.Issue(ticket.Identity{AccountID: id.AccountID, LinkID: id.LinkID, OnlineID: id.OnlineID, Country: "us", Service: "IV0001-NPXS01001_00"}, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	svc := &community.Service{Core: core, Tickets: signer, Client: "pier", OS: "linux"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- (&Server{Service: svc, PollInterval: 25 * time.Millisecond}).Serve(ctx, ln) }()
	conn, e := net.Dial("tcp", ln.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	reader := bufio.NewReader(conn)
	d := xml.NewDecoder(reader)
	send := func(s string) {
		t.Helper()
		conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
		if _, e := conn.Write([]byte(s)); e != nil {
			t.Fatal(e)
		}
	}
	receive := func(want string) node {
		t.Helper()
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for i := 0; i < 100; i++ {
			n, e := nextNode(d)
			if e != nil {
				t.Fatal("receive", want, e)
			}
			if n.Name.Local == want {
				return n
			}
		}
		t.Fatal("missing stanza", want)
		return node{}
	}
	open := `<stream:stream xmlns="jabber:client" xmlns:stream="http://etherx.jabber.org/streams" to="us.us.np.playstation.net" version="1.0">`
	send(open)
	if _, e = streamStart(d); e != nil {
		t.Fatal(e)
	}
	receive("features")
	send(`<auth xmlns="` + saslNS + `" mechanism="I5R-TICKET-AUTH">` + base64.StdEncoding.EncodeToString(raw) + `</auth>`)
	receive("success")
	d = xml.NewDecoder(reader)
	send(open)
	if _, e = streamStart(d); e != nil {
		t.Fatal(e)
	}
	receive("features")
	send(`<iq type="set" id="bind"><bind xmlns="` + bindNS + `"><resource>ps3</resource></bind></iq>`)
	receive("iq")
	send(`<iq type="get" id="roster"><query xmlns="` + rosterNS + `"/></iq>`)
	var roster node
	for i := 0; i < 20; i++ {
		n := receive("iq")
		if n.attr("id") == "roster" {
			roster = n
			break
		}
	}
	query := roster.child(rosterNS, "query")
	if query == nil || len(query.Children) != 1 || !strings.HasPrefix(query.Children[0].attr("jid"), "Bob@") {
		t.Fatalf("missing OpenPak friend: %+v", roster)
	}
	if _, e = core.GetLinkByAccount(ctx, "b"); e != nil {
		t.Fatal("friend without PS3 login was not migrated", e)
	}
	send(`<presence><status>On XMB</status></presence>`)
	deadline := time.Now().Add(3 * time.Second)
	for f.Called("Sessions.RegisterSession") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	online, e := core.Presence(ctx, []string{"a"})
	if e != nil || len(online) != 1 || online[0].Client != "pier" {
		t.Fatal("core presence not registered", online, e)
	}
	friendSession, _, e := core.RegisterPresence(ctx, "b", "switch-game", "other-adapter", "harbor", "linux")
	if e != nil {
		t.Fatal(e)
	}
	defer core.ExpirePresence(ctx, friendSession)
	for i := 0; i < 20; i++ {
		n := receive("presence")
		if strings.HasPrefix(n.attr("from"), "Bob@") && n.attr("type") == "" {
			if n.child(clientNS, "status") == nil {
				t.Fatal("missing status")
			}
			break
		}
		if i == 19 {
			t.Fatal("no friend presence")
		}
	}
	f.SetBlocked("b", "a", true)
	removed := false
	for i := 0; i < 20; i++ {
		n := receive("iq")
		if q := n.child(rosterNS, "query"); q != nil {
			for _, item := range q.Children {
				if item.attr("subscription") == "remove" {
					removed = true
				}
			}
		}
		if removed {
			break
		}
	}
	if !removed {
		t.Fatal("blocked friend remained on roster")
	}
	conn.Close()
	deadline = time.Now().Add(3 * time.Second)
	for f.Called("Sessions.ExpireSession") == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	online, e = core.Presence(ctx, []string{"a"})
	if e != nil || len(online) != 0 {
		t.Fatal("disconnect did not expire core presence", online, e)
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server failed to shut down")
	}
}
func TestXMLBounds(t *testing.T) {
	for _, input := range []string{`<iq>` + strings.Repeat(`<x>`, 20) + strings.Repeat(`</x>`, 20) + `</iq>`, `<iq>` + strings.Repeat("a", 40000) + `</iq>`} {
		if _, e := nextNode(xml.NewDecoder(strings.NewReader(input))); e == nil {
			t.Fatal("unbounded stanza accepted")
		}
	}
}

func TestXMLTokenByteLimit(t *testing.T) {
	q := &quotaReader{r: strings.NewReader(`<iq id="` + strings.Repeat("a", 100000) + `"/>`)}
	q.reset()
	if _, e := nextNode(xml.NewDecoder(q)); e == nil {
		t.Fatal("oversized attribute accepted")
	}
}

func TestIdleStreamReceivesServerKeepalive(t *testing.T) {
	const key = "core-key-core-key-core-key-core-key"
	f := fakecore.New(key)
	f.AddAccount(fakecore.Account{ID: "idle", DisplayName: "Idle"})
	core, e := coreclient.Dial("passthrough:///core", key, f.Serve(t)...)
	if e != nil {
		t.Fatal(e)
	}
	defer core.Close()
	link, e := core.ClaimOnlineID(context.Background(), "idle", "Idle")
	if e != nil {
		t.Fatal(e)
	}
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	c := &client{s: &Server{Service: &community.Service{Core: core}, Log: slog.Default(), PollInterval: 10 * time.Millisecond, KeepAliveInterval: 20 * time.Millisecond}, conn: server, id: ticket.Identity{AccountID: "idle", LinkID: link.LinkID, OnlineID: "Idle", Expires: time.Now().Add(time.Hour).UnixMilli()}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); c.maintain(ctx) }()
	// No incoming stanzas and no available presence: server traffic must still
	// arrive repeatedly, so the firmware's receive-idle timer can stay alive.
	for i := 0; i < 3; i++ {
		peer.SetReadDeadline(time.Now().Add(time.Second))
		var b [1]byte
		if _, e := peer.Read(b[:]); e != nil || b[0] != '\n' {
			t.Fatalf("missing idle heartbeat %d: %q %v", i, b, e)
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("keepalive did not stop")
	}
}

// Whitespace must keep the transport alive even when XML has no stanza to emit.
func TestIdleReaderCountsWhitespace(t *testing.T) {
	server, peer := net.Pipe()
	defer server.Close()
	defer peer.Close()
	r := &idleReader{Conn: server, timeout: 200 * time.Millisecond}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 5; i++ {
			time.Sleep(60 * time.Millisecond)
			if _, err := peer.Write([]byte("\n")); err != nil {
				done <- err
				return
			}
		}
		_, err := peer.Write([]byte(`<iq id="after-idle"/>`))
		done <- err
	}()
	n, err := nextNode(xml.NewDecoder(r))
	if err != nil {
		t.Fatal(err)
	}
	if n.attr("id") != "after-idle" {
		t.Fatal("missing stanza after keepalives")
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err := r.Read(b[:]); err == nil {
		t.Fatal("silent peer did not time out")
	}
}

func TestOfflineActivityUsesUTC(t *testing.T) {
	stamp := time.Date(2026, 10, 15, 12, 1, 32, 0, time.UTC)
	n, err := nextNode(xml.NewDecoder(strings.NewReader(offlineActivity(stamp.Unix()))))
	if err != nil || n.Name.Space != "jabber:x:delay" || n.attr("stamp") != "2026-10-15T12:01:32Z" {
		t.Fatalf("offline activity: %+v %v", n, err)
	}
	if offlineActivity(0) != "" {
		t.Fatal("unknown activity must not become epoch")
	}
}

func TestPresenceDisplayName(t *testing.T) {
	for _, tc := range []struct{ namespace, client, want string }{
		{"switch", "harbor", "Harbor"}, {"np", "pier", "Pier"},
		{"xbox360", "wharf", "Wharf"}, {"wiiu", "cemu", "Cemu"},
		{"wii", "dolphin", "Dolphin"}, {"wii", "", "Wii"},
		{"wiiu", "", "Wii U"}, {"switch", "", "Switch"},
		{"nds", "", "NDS"}, {"3ds", "", "3DS"},
		{"np", "unknown", "PS3"}, {"unknown", "", "OpenPak"},
	} {
		if got := presenceStatus(tc.namespace, tc.client, ""); got != "Online on "+tc.want {
			t.Errorf("presenceStatus(%q, %q) = %q", tc.namespace, tc.client, got)
		}
	}
	if got := presenceStatus("switch", "Harbor", "GAME"); got != "Online on Harbor — GAME" {
		t.Fatal(got)
	}
}

func TestPresenceStatusDropsRawTitleID(t *testing.T) {
	if got := presenceStatus("switch", "", "01009b500007c000"); got != "Online on Switch" {
		t.Fatalf("raw title id leaked: %q", got)
	}
	if got := presenceStatus("switch", "harbor", "Terraria"); got != "Online on Harbor — Terraria" {
		t.Fatalf("named title lost: %q", got)
	}
}

func TestDisconnectClosesOnlyThatAccountsStreams(t *testing.T) {
	s := &Server{clients: map[*client]struct{}{}}
	mk := func(acct string) net.Conn {
		a, b := net.Pipe()
		c := &client{s: s, conn: a}
		c.id.AccountID = acct
		s.clients[c] = struct{}{}
		return b
	}
	gone, kept := mk("a"), mk("b")
	defer kept.Close()
	if n := s.Disconnect("a"); n != 1 {
		t.Fatalf("closed %d", n)
	}
	gone.SetReadDeadline(time.Now().Add(time.Second))
	if _, err := gone.Read(make([]byte, 1)); err == nil {
		t.Fatal("a's stream is still open")
	}
	if s.Disconnect("") != 0 {
		t.Fatal("an empty account matches nothing")
	}
}
