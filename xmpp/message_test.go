package xmpp

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/xml"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"openpak/np-common/coreclient"
	"openpak/np-common/fakecore"
	"openpak/np-common/identity"
	"openpak/np-common/ticket"
	"openpak/np-community/community"
	"openpak/np-community/internal/fakechat"
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
	chat   *fakechat.Chat
	inbox  *community.MemoryInbox
	svc    *community.Service
	srv    *Server
	poll   *fakePoll
	logs   *syncBuf
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// fakePoll is the core event stream: version-ordered events appended by the chat announcement hook.
type fakePoll struct {
	mu  sync.Mutex
	evs []coreclient.Event
}

func (f *fakePoll) emit(id int64, to string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := uint64(len(f.evs) + 1)
	f.evs = append(f.evs, coreclient.Event{Version: v, Type: "message", AccountID: to, SubjectID: strconv.FormatInt(id, 10)})
}

func (f *fakePoll) PollEvents(_ context.Context, since uint64, limit int) ([]coreclient.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []coreclient.Event
	for _, e := range f.evs {
		if e.Version > since && len(out) < limit {
			out = append(out, e)
		}
	}
	return out, nil
}

// newRig builds the service with a fake core and a fake chat; follow starts the event follower.
func newRig(t *testing.T, setup func(*fakecore.Core), follow bool) *rig {
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
	chat := fakechat.New("chat-key")
	chat.AddFriend("a", "b")
	chat.AddFriend("a", "c")
	poll := &fakePoll{}
	chat.Announce = poll.emit
	inbox := &community.MemoryInbox{}
	svc := &community.Service{Core: core, Tickets: signer, Client: "pier", Chat: chat.Dial(t), Inbox: inbox}
	logs := &syncBuf{}
	srv := &Server{Service: svc, PollInterval: time.Hour, Log: slog.New(slog.NewTextHandler(logs, nil))}
	go srv.Serve(ctx, ln)
	if follow {
		go srv.FollowChat(ctx, poll, 20*time.Millisecond)
		for i := 0; i < 100; i++ { // first start fast-forwards; events must come after
			if _, ok, _ := inbox.Version(ctx); ok {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return &rig{t: t, core: core, signer: signer, addr: ln.Addr().String(), ctx: ctx, chat: chat, inbox: inbox, svc: svc, srv: srv, poll: poll, logs: logs}
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

func (r *rig) waitChat(n int) {
	r.t.Helper()
	for i := 0; i < 100; i++ {
		r.chat.Lock()
		got := len(r.chat.Msgs)
		r.chat.Unlock()
		if got >= n {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	r.t.Fatalf("chat never reached %d messages", n)
}

func (r *rig) fetch(p *peer, id string) {
	p.send(`<iq type="get" id="` + id + `"><fetch xmlns="` + storedNS + `"/></iq>`)
	p.next("iq")
}

func TestOutboundGoesToChatWithJoinedText(t *testing.T) {
	r := newRig(t, friends, false)
	bob := jidOf(t, r, "b")
	alice := r.login("a")
	// Spoofed from and a forged stored marker must not matter.
	alice.send(`<message to="` + bob + `" from="Mallory@us.np.community.playstation.net" id="m1" type="normal">` + attachmentBody +
		`<x xmlns="http://playstation.net/np/rt/stored" stamp="2000-01-01T00:00:00Z" delay="1"/></message>`)
	r.waitChat(1)
	m := r.chat.Msgs[0]
	if m.FromAccount != "a" || m.ToAccount != "b" || m.Kind != "text" || m.Body != "Hi\nhello" {
		t.Fatalf("chat row %v", m)
	}
	// Re-sending the same stanza is idempotent (chat's dedup key).
	alice.send(`<message to="` + bob + `" id="m1" type="normal">` + attachmentBody + `</message>`)
	time.Sleep(200 * time.Millisecond)
	r.chat.Lock()
	defer r.chat.Unlock()
	if len(r.chat.Msgs) != 1 || r.chat.Sends != 1 {
		t.Fatalf("duplicate stored: %d", len(r.chat.Msgs))
	}
	if !strings.Contains(r.logs.String(), "NP message out") || strings.Contains(r.logs.String(), "hello") {
		t.Fatalf("log: %s", r.logs.String())
	}
}

func TestAttachmentsSurvivePS3ToPS3Live(t *testing.T) {
	r := newRig(t, friends, true)
	bob := jidOf(t, r, "b")
	b := r.login("b")
	alice := r.login("a")
	alice.send(`<message to="` + bob + `/ps3" id="m1" type="chat">` + attachmentBody + `</message>`)
	m := b.next("message")
	if !strings.HasPrefix(m.attr("from"), "Alice@") || !strings.HasSuffix(m.attr("from"), "/ps3") || m.attr("id") != "m1" {
		t.Fatalf("bad envelope %s", encoded(m))
	}
	if sub := m.child(clientNS, "subject"); sub == nil || sub.Text != "Hi" {
		t.Fatalf("lost subject %s", encoded(m))
	}
	if body := m.child(clientNS, "body"); body == nil || body.Text != "hello" {
		t.Fatalf("lost body %s", encoded(m))
	}
	content := m.child("http://playstation.net/np/rt/content", "x")
	if content == nil || content.attr("context") != "NPWR00001_00" || len(content.Children) != 2 {
		t.Fatalf("lost content %s", encoded(m))
	}
	if k := m.child(messageNS, "x"); k == nil || k.attr("message_kind") != "inv" {
		t.Fatalf("lost message_kind %s", encoded(m))
	}
	if m.child(storedNS, "x") != nil {
		t.Fatal("live message carries a stored marker")
	}
	// Delivered: nothing replays, and the cursor moved.
	r.fetch(b, "f")
	b.quiet(200 * time.Millisecond)
	if c, ok, _ := r.inbox.Cursor(r.ctx, "b"); !ok || c != 1 {
		t.Fatalf("cursor %d %v", c, ok)
	}
	logs := r.logs.String()
	for _, want := range []string{"NP stream authenticated", "NP message out", "NP message in", "NP stored fetch"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("missing log %q in %s", want, logs)
		}
	}
}

func TestOfflineMessageReplayedOnFetchWithStoredMarker(t *testing.T) {
	r := newRig(t, friends, true)
	bob := jidOf(t, r, "b")
	alice := r.login("a")
	alice.send(`<message to="` + bob + `" id="m1" type="normal">` + attachmentBody + `</message>`)
	r.waitChat(1)
	time.Sleep(200 * time.Millisecond) // the follower saw it and left it pending
	if c, ok, _ := r.inbox.Cursor(r.ctx, "b"); !ok || c != 0 {
		t.Fatalf("expected pending cursor 0, got %d %v", c, ok)
	}
	if !strings.Contains(r.logs.String(), "NP message pending") {
		t.Fatalf("no pending log: %s", r.logs.String())
	}
	b := r.login("b")
	b.send(`<iq type="get" id="f1"><fetch xmlns="` + storedNS + `"/></iq>`)
	b.next("iq")
	m := b.next("message")
	if m.attr("id") != "m1" || m.child(clientNS, "body").Text != "hello" {
		t.Fatalf("replay %s", encoded(m))
	}
	st := m.child(storedNS, "x")
	if st == nil || st.attr("delay") != "1" || st.attr("id") != "1" || st.attr("expire") == "" {
		t.Fatalf("stored marker %s", encoded(m))
	}
	stamp, e := time.Parse(stampLayout, st.attr("stamp"))
	if e != nil || time.Since(stamp) > time.Minute {
		t.Fatalf("stamp %v %v", st.attr("stamp"), e)
	}
	if c, _, _ := r.inbox.Cursor(r.ctx, "b"); c != 1 {
		t.Fatalf("cursor not advanced: %d", c)
	}
	r.fetch(b, "f2")
	b.quiet(200 * time.Millisecond)
}

func TestMessageFromAnotherNetworkReachesPS3(t *testing.T) {
	r := newRig(t, friends, true)
	alice := r.login("a")
	// Carol writes through the website or a Wii U: only a chat row and the core's announcement exist.
	id := r.chat.Add("c", "a", "text", "line one\nline two", time.Now())
	m := alice.next("message")
	carol := jidOf(t, r, "c")
	if m.attr("from") != carol+"/ps3" || m.attr("id") != "chat-"+strconv.FormatInt(id, 10) || m.attr("type") != "chat" {
		t.Fatalf("envelope %s", encoded(m))
	}
	if m.child(clientNS, "subject").Text != "line one" || m.child(clientNS, "body").Text != "line two" {
		t.Fatalf("text %s", encoded(m))
	}
	if k := m.child(messageNS, "x"); k == nil || k.attr("message_kind") != textKind {
		t.Fatalf("message_kind %s", encoded(m))
	}
	// Single line: body only. Paintings arrive as a label, never as image bytes.
	r.chat.Add("c", "a", "text", "just text", time.Now())
	m = alice.next("message")
	if m.child(clientNS, "subject") != nil || m.child(clientNS, "body").Text != "just text" {
		t.Fatalf("single line %s", encoded(m))
	}
	r.chat.Add("c", "a", "painting", "AAAA", time.Now())
	m = alice.next("message")
	if m.child(clientNS, "body").Text != "[painting]" {
		t.Fatalf("painting %s", encoded(m))
	}
}

func TestEarlierPendingReplayedBeforeLiveMessage(t *testing.T) {
	r := newRig(t, friends, true)
	r.chat.Add("c", "a", "text", "first", time.Now()) // alice offline
	time.Sleep(200 * time.Millisecond)
	alice := r.login("a")
	r.chat.Add("c", "a", "text", "second", time.Now())
	m1 := alice.next("message")
	m2 := alice.next("message")
	if m1.child(clientNS, "body").Text != "first" || m1.child(storedNS, "x") == nil {
		t.Fatalf("first %s", encoded(m1))
	}
	if m2.child(clientNS, "body").Text != "second" || m2.child(storedNS, "x") != nil {
		t.Fatalf("second %s", encoded(m2))
	}
}

func TestHistoryBeforeTheFollowerIsNotReplayed(t *testing.T) {
	r := newRig(t, friends, true)
	r.chat.AddQuiet("c", "a", "text", "old news", time.Now())
	alice := r.login("a")
	r.fetch(alice, "f")
	alice.quiet(200 * time.Millisecond)
}

func TestFollowerStartsAtTheHeadOfTheStream(t *testing.T) {
	r := newRig(t, friends, false)
	r.chat.Add("c", "a", "text", "before the follower existed", time.Now())
	alice := r.login("a")
	go r.srv.FollowChat(r.ctx, r.poll, 20*time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	if v, ok, _ := r.inbox.Version(r.ctx); !ok || v != 1 {
		t.Fatalf("position %d %v", v, ok)
	}
	if _, ok, _ := r.inbox.Cursor(r.ctx, "a"); ok {
		t.Fatal("old event made a cursor")
	}
	r.chat.Add("c", "a", "text", "after", time.Now())
	if alice.next("message").child(clientNS, "body").Text != "after" {
		t.Fatal("new event not delivered")
	}
}

func TestReplayIsCappedAndSkipsExpired(t *testing.T) {
	r := newRig(t, friends, true)
	r.inbox.Advance(r.ctx, "a", 0)
	r.chat.AddQuiet("c", "a", "text", "ancient", time.Now().Add(-31*24*time.Hour))
	for i := 0; i < community.MaxReplay+5; i++ {
		r.chat.AddQuiet("c", "a", "text", "m"+strconv.Itoa(i), time.Now())
	}
	alice := r.login("a")
	r.fetch(alice, "f")
	first := alice.next("message").child(clientNS, "body").Text
	if first != "m5" {
		t.Fatalf("expected oldest kept to be m5, got %s", first)
	}
	for i := 1; i < community.MaxReplay; i++ {
		alice.next("message")
	}
	alice.quiet(200 * time.Millisecond)
	if c, _, _ := r.inbox.Cursor(r.ctx, "a"); c != int64(community.MaxReplay+6) {
		t.Fatalf("cursor %d", c)
	}
}

func TestMessageToNonFriendRejected(t *testing.T) {
	r := newRig(t, func(f *fakecore.Core) {
		friends(f)
		f.AddAccount(fakecore.Account{ID: "d", DisplayName: "Dave", Country: "US"})
	}, true)
	dave := jidOf(t, r, "d")
	alice := r.login("a")
	alice.send(`<message to="` + dave + `" id="x1"><body>hi</body></message>`)
	e := alice.next("message")
	if e.attr("type") != "error" || e.attr("id") != "x1" || e.child(clientNS, "error").child(stanzaNS, "not-acceptable") == nil {
		t.Fatalf("expected error, got %s", encoded(e))
	}
	if len(r.chat.Msgs) != 0 {
		t.Fatal("stored for a non-friend")
	}
}

func TestChatSaysNotFriendsIsNotAcceptable(t *testing.T) {
	// Friends in the core's view the roster uses, but chat (the authority) disagrees.
	r := newRig(t, friends, true)
	r.chat.Unfriend("a", "b")
	bob := jidOf(t, r, "b")
	alice := r.login("a")
	alice.send(`<message to="` + bob + `" id="x1"><body>hi</body></message>`)
	e := alice.next("message")
	if e.child(clientNS, "error").child(stanzaNS, "not-acceptable") == nil {
		t.Fatalf("got %s", encoded(e))
	}
}

func TestChatUnreachableFailsTheStanza(t *testing.T) {
	r := newRig(t, friends, true)
	bob := jidOf(t, r, "b")
	alice := r.login("a")
	r.chat.SetDown(true)
	alice.send(`<message to="` + bob + `" id="x1"><body>hi</body></message>`)
	e := alice.next("message")
	er := e.child(clientNS, "error")
	if er.attr("type") != "wait" || er.child(stanzaNS, "internal-server-error") == nil {
		t.Fatalf("got %s", encoded(e))
	}
	// And inbound fetch does not kill the stream.
	r.fetch(alice, "f")
}

func TestNoChatConfiguredIsServiceUnavailable(t *testing.T) {
	r := newRig(t, friends, false)
	r.svc.Chat = nil
	bob := jidOf(t, r, "b")
	alice := r.login("a")
	alice.send(`<message to="` + bob + `" id="x1"><body>hi</body></message>`)
	e := alice.next("message")
	if e.child(clientNS, "error").child(stanzaNS, "service-unavailable") == nil {
		t.Fatalf("got %s", encoded(e))
	}
	r.fetch(alice, "f")
}

func TestAttachmentOnlyStanzaGetsPlaceholderText(t *testing.T) {
	r := newRig(t, friends, false)
	bob := jidOf(t, r, "b")
	alice := r.login("a")
	alice.send(`<message to="` + bob + `" id="inv"><x xmlns="http://playstation.net/np/rt/message" message_kind="inv"/></message>`)
	r.waitChat(1)
	if r.chat.Msgs[0].Body != attachmentText {
		t.Fatalf("body %q", r.chat.Msgs[0].Body)
	}
	alice.send(`<message to="` + bob + `" id="empty"></message>`)
	e := alice.next("message")
	if e.child(clientNS, "error").child(stanzaNS, "bad-request") == nil {
		t.Fatalf("got %s", encoded(e))
	}
}

func TestSignInRosterPresenceAreLogged(t *testing.T) {
	r := newRig(t, friends, false)
	alice := r.login("a")
	alice.send(`<iq type="get" id="r"><query xmlns="` + rosterNS + `"/></iq>`)
	alice.next("iq")
	alice.send(`<presence><status>Online</status></presence>`)
	time.Sleep(300 * time.Millisecond)
	logs := r.logs.String()
	for _, want := range []string{"NP stream authenticated", "NP roster sent", "NP presence set", "account=a"} {
		if !strings.Contains(logs, want) {
			t.Fatalf("missing %q in %s", want, logs)
		}
	}
}
