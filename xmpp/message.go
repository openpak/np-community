package xmpp

import (
	"context"
	"encoding/xml"
	"errors"
	"strconv"
	"strings"
	"time"

	"openpak/np-community/community"
)

const (
	storedNS     = "http://playstation.net/np/rt/stored"
	maxExceedNS  = "http://playstation.net/np/rt/max_exceed"
	stampLayout  = "2006-01-02T15:04:05Z"
	maxBodyBytes = 24 << 10
)

// handleMessage routes a friend message (sceNpBasicSendMessage and game invitations
// share the <message> stanza; see research/ps3/messaging-protocol.md). The sender is
// authenticated by the stream, so a client-supplied `from` and any <x xmlns=stored>
// marker are discarded. Only accepted friends can be messaged. A recipient with no
// open stream gets the stanza stored until its next stored-fetch.
func (c *client) handleMessage(ctx context.Context, n node) error {
	switch n.attr("type") {
	case "error", "result", "groupchat", "headline":
		return nil
	}
	if !c.bound {
		return errors.New("message before bind")
	}
	reject := func(cond string, extra string) error {
		id := ""
		if v := n.attr("id"); v != "" && len(v) <= 256 {
			id = ` id=` + attr(v)
		}
		return c.send(`<message type="error"` + id + ` to=` + attr(c.jid) + `><error type="cancel"><` + cond + ` xmlns="` + stanzaNS + `"/>` + extra + `</error></message>`)
	}
	to := strings.SplitN(n.attr("to"), "/", 2)[0]
	if to == "" || c.s.Service == nil {
		return reject("bad-request", "")
	}
	var target *community.Friend
	roster, e := c.s.Service.Roster(ctx, c.id.AccountID)
	if e != nil {
		return reject("service-unavailable", "")
	}
	for i := range roster {
		if strings.EqualFold(roster[i].JID(), to) {
			target = &roster[i]
			break
		}
	}
	if target == nil {
		return reject("not-acceptable", "")
	}
	out := node{Name: n.Name, Text: ""}
	for _, a := range n.Attrs {
		switch a.Name.Local {
		case "from", "to", "xmlns", "xml":
		default:
			if a.Name.Space == "" {
				out.Attrs = append(out.Attrs, a)
			}
		}
	}
	out.Attrs = append(out.Attrs, xml.Attr{Name: xml.Name{Local: "from"}, Value: c.jid}, xml.Attr{Name: xml.Name{Local: "to"}, Value: target.JID()})
	size := 0
	for _, ch := range n.Children {
		if ch.Name.Space == storedNS {
			continue
		}
		size += len(encoded(ch))
		out.Children = append(out.Children, ch)
	}
	if size > maxBodyBytes {
		return reject("policy-violation", "")
	}
	wire := encoded(out)
	if c.s.deliver(target.AccountID, wire) > 0 {
		return nil
	}
	store := c.s.Service.Messages
	if store == nil {
		return reject("service-unavailable", "")
	}
	if _, e := store.Put(ctx, target.AccountID, wire, time.Now().UTC()); e != nil {
		if errors.Is(e, community.ErrMailboxFull) {
			return reject("resource-constraint", `<x xmlns="`+maxExceedNS+`"/>`)
		}
		c.s.Log.Warn("store message failed", "error", e)
		return reject("service-unavailable", "")
	}
	return nil
}

// deliver writes wire to every bound stream of the account and returns how many took it.
func (s *Server) deliver(account, wire string) int {
	s.mu.Lock()
	var targets []*client
	for cl := range s.clients {
		cl.stateMu.Lock()
		if cl.id.AccountID == account && cl.bound {
			targets = append(targets, cl)
		}
		cl.stateMu.Unlock()
	}
	s.mu.Unlock()
	n := 0
	for _, cl := range targets {
		if cl.send(wire) == nil {
			n++
		}
	}
	return n
}

// sendStored delivers the account's waiting messages to this stream, oldest first, each
// with the stored marker (stamp, expiry, id, delay="1"), and deletes each once written.
func (c *client) sendStored(ctx context.Context) error {
	store := c.s.Service.Messages
	if store == nil {
		return nil
	}
	c.storedMu.Lock()
	defer c.storedMu.Unlock()
	list, e := store.Pending(ctx, c.id.AccountID, time.Now().UTC())
	if e != nil {
		return e
	}
	for _, m := range list {
		marker := `<x xmlns="` + storedNS + `" id="` + strconv.FormatInt(m.ID, 10) + `" stamp="` + m.Stamp.UTC().Format(stampLayout) +
			`" expire="` + m.Expires.UTC().Format(stampLayout) + `" delay="1"/>`
		wire := strings.TrimSuffix(m.XML, "</message>") + marker + "</message>"
		if !strings.HasSuffix(m.XML, "</message>") {
			continue
		}
		if e := c.send(wire); e != nil {
			return e
		}
		if e := store.Delete(ctx, c.id.AccountID, m.ID); e != nil {
			return e
		}
	}
	return nil
}
