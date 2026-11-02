package xmpp

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/xml"
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

type peer struct {
	t    *testing.T
	conn net.Conn
	d    *xml.Decoder
}

func (p *peer) send(s string) {
	p.t.Helper()
	p.conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if _, e := p.conn.Write([]byte(s)); e != nil {
		p.t.Fatal(e)
	}
}

// next returns the next stanza named want, skipping others (presence, roster pushes).
func (p *peer) next(want string) node {
	p.t.Helper()
	p.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 100; i++ {
		n, e := nextNode(p.d)
		if e != nil {
			p.t.Fatal("receive", want, e)
		}
		if n.Name.Local == want {
			return n
		}
	}
	p.t.Fatal("missing stanza", want)
	return node{}
}

// quiet fails if a message stanza arrives within d.
func (p *peer) quiet(d time.Duration) {
	p.t.Helper()
	p.conn.SetReadDeadline(time.Now().Add(d))
	for {
		n, e := nextNode(p.d)
		if e != nil {
			return
		}
		if n.Name.Local == "message" {
			p.t.Fatalf("unexpected message %s", encoded(n))
		}
	}
}

type rig struct {
	t      *testing.T
	core   *coreclient.Client
	signer *ticket.Signer
	addr   string
	ctx    context.Context
}

func newRig(t *testing.T, store community.MessageStore, setup func(*fakecore.Core)) *rig {
	const key = "core-key-core-key-core-key-core-key"
	f := fakecore.New(key)
	setup(f)
	core, e := coreclient.Dial("passthrough:///core", key, f.Serve(t)...)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { core.Close() })
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ticket.New(priv, nil)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	svc := &community.Service{Core: core, Tickets: signer, Client: "pier", Messages: store}
	go (&Server{Service: svc, PollInterval: time.Hour, Log: nil}).Serve(ctx, ln)
	return &rig{t: t, core: core, signer: signer, addr: ln.Addr().String(), ctx: ctx}
}

func (r *rig) login(account string) *peer {
	t := r.t
	acct, e := r.core.GetAccount(r.ctx, account)
	if e != nil {
		t.Fatal(e)
	}
	id, e := identity.EnsureNPLink(r.ctx, r.core, acct)
	if e != nil {
		t.Fatal(e)
	}
	raw, e := r.signer.Issue(ticket.Identity{AccountID: id.AccountID, LinkID: id.LinkID, OnlineID: id.OnlineID, Country: "us", Service: "IV0001-NPXS01001_00"}, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	conn, e := net.Dial("tcp", r.addr)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close() })
	reader := bufio.NewReader(conn)
	p := &peer{t: t, conn: conn, d: xml.NewDecoder(reader)}
	open := `<stream:stream xmlns="jabber:client" xmlns:stream="http://etherx.jabber.org/streams" to="us.us.np.playstation.net" version="1.0">`
	p.send(open)
	if _, e = streamStart(p.d); e != nil {
		t.Fatal(e)
	}
	p.next("features")
	p.send(`<auth xmlns="` + saslNS + `" mechanism="I5R-TICKET-AUTH">` + base64.StdEncoding.EncodeToString(raw) + `</auth>`)
	p.next("success")
	p.d = xml.NewDecoder(reader)
	p.send(open)
	if _, e = streamStart(p.d); e != nil {
		t.Fatal(e)
	}
	p.next("features")
	p.send(`<iq type="set" id="bind"><bind xmlns="` + bindNS + `"><resource>ps3</resource></bind></iq>`)
	p.next("iq")
	return p
}

func friends(f *fakecore.Core) {
	f.AddAccount(fakecore.Account{ID: "a", DisplayName: "Alice", Country: "US"})
	f.AddAccount(fakecore.Account{ID: "b", DisplayName: "Bob", Country: "GB"})
	f.AddAccount(fakecore.Account{ID: "c", DisplayName: "Carol", Country: "US"})
	f.AddFriend("a", "b")
}

func jidOf(t *testing.T, r *rig, account string) string {
	acct, _ := r.core.GetAccount(r.ctx, account)
	l, e := identity.EnsureNPLink(r.ctx, r.core, acct)
	if e != nil {
		t.Fatal(e)
	}
	return community.JID(l.OnlineID, acct.Country)
}

const attachmentBody = `<subject>Hi</subject><body>hello</body>` +
	`<x xmlns="http://playstation.net/np/rt/content" context="NPWR00001_00"><header version="1">AQID</header><data type="text/plain" inline="base64">aGk=</data></x>` +
	`<x xmlns="http://playstation.net/np/rt/message" message_kind="inv"/>`

func TestMessageStoredWhileOfflineThenFetched(t *testing.T) {
	r := newRig(t, &community.MemoryStore{}, friends)
	bob := jidOf(t, r, "b")
	alice := r.login("a")
	// Spoofed from and a forged stored marker must not survive.
	alice.send(`<message to="` + bob + `" from="Mallory@us.np.community.playstation.net" id="m1" type="normal">` + attachmentBody +
		`<x xmlns="http://playstation.net/np/rt/stored" stamp="2000-01-01T00:00:00Z" delay="1"/></message>`)
	time.Sleep(200 * time.Millisecond)

	b := r.login("b")
	b.send(`<iq type="get" id="f1"><fetch xmlns="` + storedNS + `"/></iq>`)
	b.next("iq")
	m := b.next("message")
	if !strings.HasPrefix(m.attr("from"), "Alice@") || !strings.HasSuffix(m.attr("from"), "/ps3") || m.attr("id") != "m1" {
		t.Fatalf("bad envelope %s", encoded(m))
	}
	if m.child(clientNS, "body") == nil || m.child(clientNS, "body").Text != "hello" {
		t.Fatalf("lost body %s", encoded(m))
	}
	content := m.child("http://playstation.net/np/rt/content", "x")
	if content == nil || content.attr("context") != "NPWR00001_00" || content.child("", "data") == nil && len(content.Children) != 2 {
		t.Fatalf("lost content %s", encoded(m))
	}
	var stored []node
	for _, ch := range m.Children {
		if ch.Name.Space == storedNS {
			stored = append(stored, ch)
		}
	}
	if len(stored) != 1 || stored[0].attr("delay") != "1" || stored[0].attr("stamp") == "2000-01-01T00:00:00Z" || stored[0].attr("expire") == "" || stored[0].attr("id") == "" {
		t.Fatalf("stored marker %v", stored)
	}
	if _, e := time.Parse(stampLayout, stored[0].attr("stamp")); e != nil {
		t.Fatal(e)
	}
	// Delivered once.
	b.send(`<iq type="get" id="f2"><fetch xmlns="` + storedNS + `"/></iq>`)
	b.next("iq")
	b.quiet(200 * time.Millisecond)
}

func TestMessageLiveDeliveryNeedsNoStore(t *testing.T) {
	r := newRig(t, &community.MemoryStore{}, friends)
	bob := jidOf(t, r, "b")
	b := r.login("b")
	alice := r.login("a")
	alice.send(`<message to="` + bob + `/ps3" type="chat">` + attachmentBody + `</message>`)
	m := b.next("message")
	for _, ch := range m.Children {
		if ch.Name.Space == storedNS {
			t.Fatal("live message carries a stored marker")
		}
	}
	b.send(`<iq type="get" id="f"><fetch xmlns="` + storedNS + `"/></iq>`)
	b.next("iq")
	b.quiet(200 * time.Millisecond)
}

func TestMessageToNonFriendRejected(t *testing.T) {
	r := newRig(t, &community.MemoryStore{}, friends)
	carol := jidOf(t, r, "c")
	alice := r.login("a")
	alice.send(`<message to="` + carol + `" id="x1"><body>hi</body></message>`)
	e := alice.next("message")
	if e.attr("type") != "error" || e.attr("id") != "x1" || e.child(clientNS, "error") == nil {
		t.Fatalf("expected error, got %s", encoded(e))
	}
	// Nothing was queued for Carol.
	c := r.login("c")
	c.send(`<iq type="get" id="f"><fetch xmlns="` + storedNS + `"/></iq>`)
	c.next("iq")
	c.quiet(200 * time.Millisecond)
}

func TestMailboxFullReportsMaxExceed(t *testing.T) {
	store := &community.MemoryStore{}
	r := newRig(t, store, friends)
	bob := jidOf(t, r, "b")
	alice := r.login("a")
	for i := 0; i < community.MaxStoredPerRecipient; i++ {
		alice.send(`<message to="` + bob + `"><body>x</body></message>`)
	}
	alice.send(`<message to="` + bob + `" id="over"><body>x</body></message>`)
	e := alice.next("message")
	if e.attr("id") != "over" || e.attr("type") != "error" {
		t.Fatalf("got %s", encoded(e))
	}
	if !strings.Contains(encoded(e), maxExceedNS) {
		t.Fatalf("no max_exceed in %s", encoded(e))
	}
}

func TestMemoryStoreExpiryAndDelete(t *testing.T) {
	m := &community.MemoryStore{}
	ctx := context.Background()
	now := time.Now().UTC()
	s1, _ := m.Put(ctx, "x", "<message/>", now)
	s2, _ := m.Put(ctx, "x", "<message/>", now.Add(time.Hour))
	got, _ := m.Pending(ctx, "x", now.Add(community.StoredTTL+30*time.Minute))
	if len(got) != 1 || got[0].ID != s2.ID {
		t.Fatalf("expiry: %v", got)
	}
	m.Delete(ctx, "x", s1.ID)
	got, _ = m.Pending(ctx, "x", now)
	if len(got) != 1 || got[0].ID != s2.ID {
		t.Fatalf("delete: %v", got)
	}
}
