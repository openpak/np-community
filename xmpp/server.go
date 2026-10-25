package xmpp

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"regexp"
	"strings"
	"sync"
	"time"

	"openpak/np-common/ticket"
	"openpak/np-community/community"
)

type Server struct {
	Service           *community.Service
	Log               *slog.Logger
	PollInterval      time.Duration
	KeepAliveInterval time.Duration
	mu                sync.Mutex
	clients           map[*client]struct{}
}
type client struct {
	s                *Server
	conn             net.Conn
	writeMu          sync.Mutex
	stateMu          sync.Mutex
	id               ticket.Identity
	jid              string
	bound, available bool
	session          string
	lease            int32
	presence         []node
	snapshot         map[string]string
	roster           map[string]community.Friend
}

func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	if s.KeepAliveInterval == 0 {
		s.KeepAliveInterval = 20 * time.Second
	}
	if s.PollInterval == 0 {
		s.PollInterval = 10 * time.Second
	}
	s.mu.Lock()
	s.clients = map[*client]struct{}{}
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		ln.Close()
		s.mu.Lock()
		for c := range s.clients {
			c.conn.Close()
		}
		s.mu.Unlock()
	}()
	for {
		conn, e := ln.Accept()
		if e != nil {
			if ctx.Err() != nil {
				return nil
			}
			return e
		}
		c := &client{s: s, conn: conn, snapshot: map[string]string{}, roster: map[string]community.Friend{}}
		s.mu.Lock()
		if len(s.clients) >= 1024 {
			s.mu.Unlock()
			conn.Close()
			continue
		}
		s.clients[c] = struct{}{}
		s.mu.Unlock()
		go c.run(ctx)
	}
}

// Disconnect closes every stream signed in as the account and returns how
// many there were. Each stream's own teardown expires its presence lease.
func (s *Server) Disconnect(account string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for c := range s.clients {
		c.stateMu.Lock()
		mine := c.id.AccountID == account
		c.stateMu.Unlock()
		if mine && account != "" {
			c.conn.Close()
			n++
		}
	}
	return n
}

func (c *client) send(text string) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, e := fmt.Fprint(c.conn, text)
	return e
}
func (c *client) run(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	defer c.conn.Close()
	defer func() { c.expire(); c.s.mu.Lock(); delete(c.s.clients, c); c.s.mu.Unlock() }()
	c.conn.SetReadDeadline(time.Now().Add(30 * time.Second))
	idle := &idleReader{Conn: c.conn, timeout: 30 * time.Second}
	reader := bufio.NewReaderSize(idle, 16384)
	quota := &quotaReader{r: reader}
	quota.reset()
	d := xml.NewDecoder(quota)
	if _, e := streamStart(d); e != nil {
		return
	}
	if c.send(`<?xml version="1.0"?><stream:stream xmlns="jabber:client" xmlns:stream="http://etherx.jabber.org/streams" id="openpak" version="1.0"><stream:features><mechanisms xmlns="`+saslNS+`"><mechanism>I5R-TICKET-AUTH</mechanism></mechanisms></stream:features>`) != nil {
		return
	}
	n, e := nextNode(d)
	if e != nil {
		return
	}
	if n.Name.Space != saslNS || n.Name.Local != "auth" || n.attr("mechanism") != "I5R-TICKET-AUTH" {
		c.authFailure()
		return
	}
	raw, e := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(n.Text), ""))
	if e != nil {
		c.authFailure()
		return
	}
	id, e := c.s.Service.Verify(ctx, raw)
	if e != nil {
		c.authFailure()
		return
	}
	c.stateMu.Lock()
	c.id = id
	c.stateMu.Unlock()
	if c.send(`<success xmlns="`+saslNS+`"/>`) != nil {
		return
	}
	quota.reset()
	d = xml.NewDecoder(quota)
	opening, e := streamStart(d)
	if e != nil {
		return
	}
	domain := ""
	for _, a := range opening.Attr {
		if a.Name.Local == "to" {
			domain = a.Value
		}
	}
	// The firmware derives the stream domain from profile/ticket metadata. Accept
	// only a bounded PlayStation domain, and never use it as a lookup authority.
	if len(domain) > 128 || !strings.HasSuffix(domain, ".playstation.net") {
		return
	}
	c.jid = id.OnlineID + "@" + domain + "/ps3"
	if c.send(`<stream:stream xmlns="jabber:client" xmlns:stream="http://etherx.jabber.org/streams" from=`+attr(domain)+` id="openpak-session" version="1.0"><stream:features><bind xmlns="`+bindNS+`"/><session xmlns="`+sessionNS+`"/></stream:features>`) != nil {
		return
	}
	go c.maintain(ctx)
	c.s.Log.Info("NP stream authenticated", "online_id", id.OnlineID)
	// Incoming whitespace is activity too: XML may wait indefinitely for a stanza.
	idle.timeout = 15 * time.Minute
	c.conn.SetReadDeadline(time.Time{})
	for {
		quota.reset()
		n, e = nextNode(d)
		if e != nil {
			c.s.Log.Info("NP stream read ended", "online_id", c.id.OnlineID, "error", e)
			return
		}
		if time.Now().UnixMilli() >= id.Expires {
			return
		}
		if e = c.handle(ctx, n); e != nil {
			if !errors.Is(e, net.ErrClosed) {
				c.s.Log.Info("NP stream ended", "online_id", id.OnlineID, "error", e)
			}
			return
		}
	}
}

// Refresh the deadline on socket reads rather than complete XML stanzas, since
// firmware can send only whitespace keepalives for an otherwise healthy session.
type idleReader struct {
	net.Conn
	timeout time.Duration
}

func (r *idleReader) Read(p []byte) (int, error) {
	timeout := r.timeout
	if timeout == 0 {
		timeout = 15 * time.Minute
	}
	r.SetReadDeadline(time.Now().Add(timeout))
	return r.Conn.Read(p)
}
func (c *client) authFailure() { c.send(`<failure xmlns="` + saslNS + `"><not-authorized/></failure>`) }
func (c *client) iq(n node, body string) error {
	return c.send(`<iq type="result" id=` + attr(n.attr("id")) + `>` + body + `</iq>`)
}
func (c *client) iqError(n node, condition string) error {
	return c.send(`<iq type="error" id=` + attr(n.attr("id")) + `><error type="cancel"><` + condition + ` xmlns="` + stanzaNS + `"/></error></iq>`)
}
func (c *client) handle(ctx context.Context, n node) error {
	if n.Name.Space != clientNS && n.Name.Space != "" {
		return fmt.Errorf("invalid stanza namespace")
	}
	switch n.Name.Local {
	case "iq":
		kind := n.attr("type")
		if kind == "result" || kind == "error" {
			return nil
		}
		if len(n.attr("id")) > 256 {
			return fmt.Errorf("invalid IQ id")
		}
		if bind := n.child(bindNS, "bind"); bind != nil {
			if kind != "set" || c.bound {
				return c.iqError(n, "not-allowed")
			}
			resource := "ps3"
			if r := bind.child(bindNS, "resource"); r != nil && r.Text != "" {
				resource = r.Text
			}
			if len(resource) > 4 || strings.ContainsAny(resource, "/@\x00") {
				return c.iqError(n, "bad-request")
			}
			c.jid = strings.SplitN(c.jid, "/", 2)[0] + "/" + resource
			c.stateMu.Lock()
			c.bound = true
			c.stateMu.Unlock()
			return c.iq(n, `<bind xmlns="`+bindNS+`"><jid>`+escaped(c.jid)+`</jid></bind>`)
		}
		if !c.bound {
			return c.iqError(n, "not-authorized")
		}
		if n.child(sessionNS, "session") != nil {
			return c.iq(n, "")
		}
		if query := n.child(rosterNS, "query"); query != nil {
			if kind == "set" {
				for _, item := range query.Children {
					if item.attr("subscription") != "remove" {
						return c.iqError(n, "not-allowed")
					}
					target, e := c.s.Service.ResolveTarget(ctx, c.id.AccountID, item.attr("jid"))
					if e != nil || c.s.Service.Core.RemoveFriend(ctx, c.id.AccountID, target) != nil {
						return c.iqError(n, "service-unavailable")
					}
				}
				return c.iq(n, "")
			}
			if kind != "get" {
				return c.iqError(n, "bad-request")
			}
			return c.rosterReply(ctx, n)
		}
		if n.child("http://playstation.net/np/rt/stored", "fetch") != nil {
			return c.iq(n, `<fetch xmlns="http://playstation.net/np/rt/stored"/>`)
		}
		if q := n.child("jabber:iq:privacy", "query"); q != nil && kind == "get" {
			return c.iq(n, `<query xmlns="jabber:iq:privacy"/>`)
		}
		if n.child("urn:xmpp:ping", "ping") != nil {
			return c.iq(n, "")
		}
		return c.iqError(n, "service-unavailable")
	case "presence":
		if !c.bound {
			return fmt.Errorf("presence before bind")
		}
		return c.onPresence(ctx, n)
	case "message":
		return c.send(`<message type="error" to=` + attr(c.jid) + `><error type="cancel"><service-unavailable xmlns="` + stanzaNS + `"/></error></message>`)
	}
	return nil
}
func (c *client) expire() {
	c.stateMu.Lock()
	id := c.session
	c.session = ""
	c.available = false
	c.stateMu.Unlock()
	if id != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c.s.Service.Core.ExpirePresence(ctx, id)
	}
}
func (c *client) maintain(ctx context.Context) {
	tick := time.NewTicker(min(c.s.PollInterval, c.s.KeepAliveInterval, time.Second))
	defer tick.Stop()
	lastPoll := time.Time{}
	nextBeat := time.Time{}
	nextKeepAlive := time.Now().Add(c.s.KeepAliveInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			if !now.Before(nextKeepAlive) {
				if e := c.send("\n"); e != nil {
					c.s.Log.Info("NP stream keepalive failed", "online_id", c.id.OnlineID, "error", e)
					c.conn.Close()
					return
				}
				nextKeepAlive = now.Add(c.s.KeepAliveInterval)
			}
			if now.UnixMilli() >= c.id.Expires {
				c.conn.Close()
				return
			}
			c.stateMu.Lock()
			session := c.session
			lease := c.lease
			c.stateMu.Unlock()
			if session != "" && now.After(nextBeat) {
				n, e := c.s.Service.Core.HeartbeatPresence(ctx, session)
				if e != nil {
					c.conn.Close()
					return
				}
				if n > 0 {
					lease = n
				}
				nextBeat = now.Add(time.Duration(max(1, lease/3)) * time.Second)
			}
			if now.Sub(lastPoll) >= c.s.PollInterval {
				// Recheck bans/link revocation even for quiet sessions.
				if e := c.s.Service.Recheck(ctx, c.id); e != nil {
					if ctx.Err() != nil {
						return
					}
					c.s.Log.Warn("NP stream identity check failed", "online_id", c.id.OnlineID, "error", e)
					c.conn.Close()
					return
				}
				if e := c.refresh(ctx); e != nil {
					if ctx.Err() != nil {
						return
					}
					c.s.Log.Warn("NP stream refresh failed", "online_id", c.id.OnlineID, "error", e)
					c.conn.Close()
					return
				}
				lastPoll = now
			}
		}
	}
}
func (c *client) onPresence(ctx context.Context, n node) error {
	switch n.attr("type") {
	case "unavailable":
		c.expire()
		return nil
	case "subscribe", "subscribed", "unsubscribe", "unsubscribed":
		target, e := c.s.Service.ResolveTarget(ctx, c.id.AccountID, n.attr("to"))
		if e != nil {
			return e
		}
		switch n.attr("type") {
		case "subscribe":
			e = c.s.Service.Core.RequestFriend(ctx, c.id.AccountID, target)
		case "subscribed":
			e = c.s.Service.Core.AcceptFriend(ctx, c.id.AccountID, target)
		default:
			e = c.s.Service.Core.RemoveFriend(ctx, c.id.AccountID, target)
		}
		if e != nil {
			return e
		}
		return c.refresh(ctx)
	case "":
	default:
		return nil
	}
	c.stateMu.Lock()
	session := c.session
	c.stateMu.Unlock()
	if session == "" {
		session, lease, e := c.s.Service.Core.RegisterPresence(ctx, c.id.AccountID, "", c.jid, c.s.Service.Client, c.s.Service.OS)
		if e != nil {
			return e
		}
		c.stateMu.Lock()
		c.session = session
		c.lease = lease
		c.stateMu.Unlock()
	}
	c.stateMu.Lock()
	c.available = true
	c.presence = n.Children
	c.stateMu.Unlock()
	return c.refresh(ctx)
}
func (c *client) rosterReply(ctx context.Context, n node) error {
	friends, e := c.s.Service.Roster(ctx, c.id.AccountID)
	if e != nil {
		return c.iqError(n, "service-unavailable")
	}
	outgoing, e := c.s.Service.Pending(ctx, c.id.AccountID, false)
	if e != nil {
		return c.iqError(n, "service-unavailable")
	}
	var b strings.Builder
	b.WriteString(`<query xmlns="` + rosterNS + `">`)
	c.stateMu.Lock()
	c.roster = map[string]community.Friend{}
	for _, f := range friends {
		c.roster[f.AccountID] = f
		b.WriteString(rosterItem(f, false))
	}
	for _, f := range outgoing {
		b.WriteString(rosterItem(f, true))
	}
	c.stateMu.Unlock()
	b.WriteString(`</query>`)
	if e = c.iq(n, b.String()); e != nil {
		return e
	}
	c.s.Log.Info("NP roster sent", "online_id", c.id.OnlineID, "friends", len(friends))
	return c.refresh(ctx)
}
func rosterItem(f community.Friend, pending bool) string {
	ask := ""
	if pending {
		ask = ` ask="subscribe" asked="subscribe"`
	}
	return `<item jid=` + attr(f.JID()) + ` name=` + attr(f.DisplayName) + ` subscription=` + attr(f.Subscription) + ask + `><group>OpenPak</group></item>`
}

// refresh polls the canonical graph and live leases, pushing roster/presence
// changes to this authenticated viewer. No graph or long-lived presence is kept here.
func (c *client) refresh(ctx context.Context) error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if !c.bound {
		return nil
	}
	friends, e := c.s.Service.Roster(ctx, c.id.AccountID)
	if e != nil {
		return e
	}
	ids := make([]string, 0, len(friends))
	for _, f := range friends {
		ids = append(ids, f.AccountID)
	}
	presence, e := c.s.Service.Core.Presence(ctx, ids)
	if e != nil {
		return e
	}
	byID := map[string]string{}
	for _, p := range presence {
		byID[p.AccountId] = presenceStatus(p.Namespace, p.Client, p.TitleId)
	}
	next := map[string]community.Friend{}
	for _, f := range friends {
		next[f.AccountID] = f
		old, exists := c.roster[f.AccountID]
		// LastSeenUnix is not part of the roster item; comparing it re-sent the item on every poll.
		if !exists || rosterItem(old, false) != rosterItem(f, false) {
			if e = c.send(`<iq type="set" id=` + attr("roster-"+f.OnlineID) + `><query xmlns="` + rosterNS + `">` + rosterItem(f, false) + `</query></iq>`); e != nil {
				return e
			}
		}
		status, online := byID[f.AccountID]
		// An online friend's last-seen moves every poll and the firmware counts each <presence>
		// as a new online friend (top-bar counter climbed), so only offline friends carry it.
		lastSeen := f.LastSeenUnix
		if online {
			lastSeen = 0
		}
		snapshot, _ := json.Marshal([]any{online, status, f.JID(), lastSeen})
		value := string(snapshot)
		if c.snapshot[f.AccountID] != value {
			kind := ""
			body := `<x xmlns="http://playstation.net/np/rt/profile"><name>` + escaped(f.DisplayName) + `</name></x>`
			if !online {
				kind = ` type="unavailable"`
				body += offlineActivity(f.LastSeenUnix)
			} else {
				body += `<status>` + escaped(status) + `</status>`
			}
			if e = c.send(`<presence from=` + attr(f.JID()+"/ps3") + ` to=` + attr(c.jid) + kind + `>` + body + `</presence>`); e != nil {
				return e
			}
			c.s.Log.Info("NP presence sent", "viewer", c.id.OnlineID, "friend", f.OnlineID, "online", online, "status", status)
			c.snapshot[f.AccountID] = value
		}
	}
	for id, f := range c.roster {
		if _, ok := next[id]; !ok {
			if e = c.send(`<presence from=` + attr(f.JID()+"/ps3") + ` type="unavailable"/><iq type="set" id=` + attr("remove-"+f.OnlineID) + `><query xmlns="` + rosterNS + `"><item jid=` + attr(f.JID()) + ` subscription="remove"/></query></iq>`); e != nil {
				return e
			}
			delete(c.snapshot, id)
		}
	}
	c.roster = next
	incoming, e := c.s.Service.Pending(ctx, c.id.AccountID, true)
	if e != nil {
		return e
	}
	for _, f := range incoming {
		key := "incoming:" + f.AccountID
		if c.snapshot[key] != "sent" {
			if e = c.send(`<presence from=` + attr(f.JID()) + ` type="subscribe"/>`); e != nil {
				return e
			}
			c.snapshot[key] = "sent"
		}
	}
	return nil
}

func offlineActivity(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return `<x xmlns="jabber:x:delay" stamp=` + attr(time.Unix(unix, 0).UTC().Format(time.RFC3339)) + `/>`
}

var hexTitleID = regexp.MustCompile(`^[0-9A-Fa-f]{16}$`)

// Prefer a recognized client name; otherwise identify the console network.
func presenceStatus(namespace, client, title string) string {
	platform := map[string]string{
		"np": "PS3", "ps3": "PS3",
		"xbox360": "Xbox 360", "xbox-360": "Xbox 360", "xbl": "Xbox 360",
		"wii": "Wii", "wiiu": "Wii U", "switch": "Switch",
		"3ds": "3DS", "nds": "NDS", "ds": "NDS",
	}[strings.ToLower(strings.TrimSpace(namespace))]
	if platform == "" {
		platform = "OpenPak"
	}
	emulator := map[string]string{
		"harbor": "Harbor", "pier": "Pier", "wharf": "Wharf", "cemu": "Cemu",
		"azahar": "Azahar", "dolphin": "Dolphin", "melonds": "melonDS",
	}[strings.ToLower(strings.TrimSpace(client))]
	if emulator != "" {
		platform = emulator
	}
	status := "Online on " + platform
	if title != "" {
		// ponytail: raw title ids (16 hex digits) are noise to a player, no name lookup exists yet
		if !hexTitleID.MatchString(title) {
			status += " — " + title
		}
	}
	return status
}
