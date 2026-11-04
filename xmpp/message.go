package xmpp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"sync"
	"time"

	"openpak/np-common/coreclient"
	"openpak/np-community/community"
)

const (
	storedNS     = "http://playstation.net/np/rt/stored"
	messageNS    = "http://playstation.net/np/rt/message"
	maxExceedNS  = "http://playstation.net/np/rt/max_exceed"
	stampLayout  = "2006-01-02T15:04:05Z"
	maxBodyBytes = 24 << 10
	// attachmentText is the chat body of a stanza that has attachments (a game invitation) but no text.
	attachmentText = "[attachment]"
	// textKind is the np/rt/message message_kind written on messages that did not come from a PS3. The
	// firmware only limits it to 3 characters; which codes it knows is not recovered, so this is a guess.
	textKind = "txt"
)

var textElems = map[string]bool{"subject": true, "body": true}

// handleMessage routes a friend message (sceNpBasicSendMessage and game invitations share the
// <message> stanza; see research/ps3/messaging-protocol.md) into the core-neutral chat service.
// The sender is authenticated by the stream, so a client-supplied `from` and any <x xmlns=stored>
// marker are discarded. Chat accepts friends only (the core's answer); the recipient's own network
// delivers it, whether or not they are connected.
//
// Mapping: chat body = subject + "\n" + body (just the one that exists, "[attachment]" if only
// attachments), kind text, dedup key = stanza id + recipient + content hash. The subject/body split, stanza
// id/type and the non-text children (np/rt/content etc.) cannot ride chat; they are kept in np_message_ext
// keyed by the chat message id and handed back only to PS3 recipients.
func (c *client) handleMessage(ctx context.Context, n node) error {
	switch n.attr("type") {
	case "error", "result", "groupchat", "headline":
		return nil
	}
	if !c.bound {
		return errors.New("message before bind")
	}
	reject := func(typ, cond string, extra string) error {
		id := ""
		if v := n.attr("id"); v != "" && len(v) <= 256 {
			id = ` id=` + attr(v)
		}
		return c.send(`<message type="error"` + id + ` to=` + attr(c.jid) + `><error type="` + typ + `"><` + cond + ` xmlns="` + stanzaNS + `"/>` + extra + `</error></message>`)
	}
	to := strings.SplitN(n.attr("to"), "/", 2)[0]
	if to == "" || c.s.Service == nil {
		return reject("modify", "bad-request", "")
	}
	svc := c.s.Service
	if svc.Chat == nil || svc.Inbox == nil {
		return reject("cancel", "service-unavailable", "")
	}
	var target *community.Friend
	roster, e := svc.Roster(ctx, c.id.AccountID)
	if e != nil {
		return reject("wait", "service-unavailable", "")
	}
	for i := range roster {
		if strings.EqualFold(roster[i].JID(), to) {
			target = &roster[i]
			break
		}
	}
	if target == nil {
		return reject("cancel", "not-acceptable", "")
	}
	ext := community.Ext{StanzaID: n.attr("id"), Type: n.attr("type")}
	if len(ext.StanzaID) > 256 {
		ext.StanzaID = ""
	}
	size := 0
	var extra strings.Builder
	for _, ch := range n.Children {
		if ch.Name.Space == storedNS {
			continue
		}
		size += len(encoded(ch))
		if (ch.Name.Space == "" || ch.Name.Space == clientNS) && textElems[ch.Name.Local] {
			if ch.Name.Local == "subject" {
				ext.Subject = ch.Text
			} else {
				ext.Body = ch.Text
			}
			continue
		}
		extra.WriteString(encoded(ch))
	}
	if size > maxBodyBytes {
		return reject("modify", "policy-violation", "")
	}
	ext.XML = extra.String()
	text := joinText(ext.Subject, ext.Body)
	if strings.TrimSpace(text) == "" {
		if ext.XML == "" {
			return reject("modify", "bad-request", "")
		}
		text = attachmentText
	}
	sum := sha256.Sum256([]byte(ext.Subject + "\x00" + ext.Body + "\x00" + ext.XML))
	dedup := ext.StanzaID + ":" + target.AccountID + ":" + hex.EncodeToString(sum[:8])
	m, dup, e := svc.Chat.Send(ctx, c.id.AccountID, target.AccountID, "text", text, dedup)
	switch {
	case errors.Is(e, community.ErrNotFriends):
		return reject("cancel", "not-acceptable", "")
	case errors.Is(e, community.ErrChatRejected):
		return reject("modify", "policy-violation", "")
	case e != nil:
		c.s.Log.Warn("NP message out failed", "account", c.id.AccountID, "to", target.AccountID, "error", e)
		return reject("wait", "internal-server-error", "")
	}
	if !dup {
		ext.ChatID = m.ID
		ext.Sender, ext.Recipient = c.id.AccountID, target.AccountID
		if e := svc.Inbox.PutExt(ctx, ext); e != nil {
			c.s.Log.Warn("NP message ext not stored; recipient gets text only", "chat_id", m.ID, "error", e)
		}
	}
	c.s.Log.Info("NP message out", "account", c.id.AccountID, "to", target.AccountID, "chat_id", m.ID, "duplicate", dup, "attachments", ext.XML != "")
	return nil
}

func joinText(subject, body string) string {
	switch {
	case subject == "":
		return body
	case body == "":
		return subject
	}
	return subject + "\n" + body
}

// splitText is joinText's best-effort inverse for messages that have no Ext: the first line is the subject.
func splitText(text string) (subject, body string) {
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		return text[:i], text[i+1:]
	}
	return "", text
}

// accountLocks serialises delivery per account so the live path and a replay cannot both send a message.
type accountLocks struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

func (l *accountLocks) get(account string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]*sync.Mutex{}
	}
	if l.m[account] == nil {
		l.m[account] = &sync.Mutex{}
	}
	return l.m[account]
}

// stanza renders one chat message as the <message> the firmware expects. liveID is the message that
// just arrived on the event stream; every other message is a replay and carries the stored marker.
func (s *Server) stanza(ctx context.Context, m community.ChatMessage, toJID string, stored bool) (string, error) {
	svc := s.Service
	fromJID, err := svc.AccountJID(ctx, m.From)
	if err != nil {
		return "", err
	}
	ext, ok, err := svc.Inbox.Ext(ctx, m.ID)
	if err != nil {
		return "", err
	}
	if !ok && !stored {
		// The sender's own stanza handler stores the Ext right after chat returns; the event can win by milliseconds.
		time.Sleep(250 * time.Millisecond)
		ext, ok, err = svc.Inbox.Ext(ctx, m.ID)
		if err != nil {
			return "", err
		}
	}
	id, typ := "chat-"+strconv.FormatInt(m.ID, 10), "chat"
	var subject, body, extra string
	switch {
	case ok && m.Kind == "text":
		subject, body, extra = ext.Subject, ext.Body, ext.XML
		if ext.StanzaID != "" {
			id = ext.StanzaID
		}
		if ext.Type != "" {
			typ = ext.Type
		}
	case m.Kind == "text":
		subject, body = splitText(m.Body)
		extra = `<x xmlns="` + messageNS + `" message_kind="` + textKind + `"/>`
	default:
		// Paintings, screenshots and app data have no PS3 form; say what arrived.
		body = "[" + strings.ReplaceAll(m.Kind, "_", " ") + "]"
		extra = `<x xmlns="` + messageNS + `" message_kind="` + textKind + `"/>`
	}
	var b strings.Builder
	b.WriteString(`<message from=` + attr(fromJID+"/ps3") + ` to=` + attr(toJID) + ` id=` + attr(id) + ` type=` + attr(typ) + `>`)
	if subject != "" {
		b.WriteString(`<subject>` + escaped(subject) + `</subject>`)
	}
	b.WriteString(`<body>` + escaped(body) + `</body>` + extra)
	if stored {
		b.WriteString(`<x xmlns="` + storedNS + `" id="` + strconv.FormatInt(m.ID, 10) + `" stamp="` + m.SentAt.UTC().Format(stampLayout) +
			`" expire="` + m.SentAt.Add(community.StoredTTL).UTC().Format(stampLayout) + `" delay="1"/>`)
	}
	b.WriteString(`</message>`)
	return b.String(), nil
}

// deliver writes wire to every bound stream of the account and returns how many took it.
func (s *Server) deliver(account, wire string) int {
	return s.each(account, nil, func(cl *client) bool { return cl.send(wire) == nil })
}

// each runs fn on the account's bound streams (only: just that one) and returns how many fn accepted.
func (s *Server) each(account string, only *client, fn func(*client) bool) int {
	s.mu.Lock()
	var targets []*client
	for cl := range s.clients {
		if only != nil && cl != only {
			continue
		}
		cl.stateMu.Lock()
		if cl.id.AccountID == account && cl.bound {
			targets = append(targets, cl)
		}
		cl.stateMu.Unlock()
	}
	s.mu.Unlock()
	n := 0
	for _, cl := range targets {
		if fn(cl) {
			n++
		}
	}
	return n
}

// drain delivers the account's pending chat messages, oldest first, to its open streams (only: to that
// stream alone) and advances the account's cursor past each one written. liveID marks the message that
// just arrived, which goes out without the stored marker. It returns how many messages were written.
func (s *Server) drain(ctx context.Context, account string, only *client, liveID int64) (int, error) {
	svc := s.Service
	lock := s.locks.get(account)
	lock.Lock()
	defer lock.Unlock()
	msgs, maxSeen, err := svc.PendingMessages(ctx, account)
	if err != nil {
		return 0, err
	}
	toJID, err := svc.AccountJID(ctx, account)
	if err != nil {
		return 0, err
	}
	now := time.Now()
	if len(msgs) > community.MaxReplay {
		for _, m := range msgs[:len(msgs)-community.MaxReplay] {
			svc.Inbox.Advance(ctx, account, m.ID)
		}
		msgs = msgs[len(msgs)-community.MaxReplay:]
	}
	sent := 0
	for _, m := range msgs {
		if now.Sub(m.SentAt) > community.StoredTTL {
			continue
		}
		wire, err := s.stanza(ctx, m, toJID, m.ID != liveID)
		if errors.Is(err, coreclient.ErrAccountDisabled) || errors.Is(err, coreclient.ErrNotFound) {
			continue // sender gone; nothing to show
		}
		if err != nil {
			return sent, err
		}
		if s.each(account, only, func(cl *client) bool { return cl.send(wire) == nil }) == 0 {
			return sent, nil // nobody could take it: stays pending
		}
		if err := svc.Inbox.Advance(ctx, account, m.ID); err != nil {
			return sent, err
		}
		sent++
	}
	// Everything pending was written or expired; step over the account's own sent messages too.
	return sent, svc.Inbox.Advance(ctx, account, maxSeen)
}

// sendStored replays the account's pending messages to this stream (the stored-fetch iq).
func (c *client) sendStored(ctx context.Context) error {
	if c.s.Service.Chat == nil || c.s.Service.Inbox == nil {
		return nil
	}
	n, err := c.s.drain(ctx, c.id.AccountID, c, 0)
	c.s.Log.Info("NP stored fetch", "account", c.id.AccountID, "delivered", n)
	if errors.Is(err, community.ErrChatUnavailable) {
		return nil // chat is the store; the next fetch retries
	}
	return err
}
