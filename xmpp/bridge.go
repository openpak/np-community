package xmpp

// The PS3 half of the Minecraft cross-play bridge (card openpak/prds#177). A player hosting a game on another console is not signed in to
// this relay, but their PS3 friends must see the game, and a PS3 that tries to reach the host sends NP signaling toward a JID nobody holds.
// common/minecraft's session directory uses three routes (bearer NP_BRIDGE_KEY):
//
//	PUT    /internal/bridge/presence/{account}  {game, title, data (hex, the 56-byte presence blob) or children (raw XML)}: the host's game
//	                                           presence, shown to every friend who sees the account online, for 90 s per PUT
//	DELETE /internal/bridge/presence/{account}  take it back
//	GET    /internal/bridge/signaling?after=N   the signaling stanzas addressed to a bridged host, in full
//
// Bridged presence lives in memory with a lease (the directory renews it every 30 s), apart from the presence data a real console stores in
// Postgres: a real console signed in on the same account keeps its own, and wins while it has any.

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"openpak/np-community/community"
)

// bridgeDataNS wraps the blob when the caller gives only bytes. GUESS: a real console's presence stanza is not captured (the relay stores its
// children verbatim); the first real hosting after v0.4.11 logs it ("NP presence xml"), and this constant is then replaced by what it shows.
const bridgeDataNS = "http://playstation.net/np/rt/presence-data"

const bridgeLease = 90 * time.Second

var accountRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

type bridgedHost struct {
	game, xml, online, account string
	jid                        string
	until                      time.Time
}

type signalEvent struct {
	Seq     int64             `json:"seq"`
	Time    time.Time         `json:"time"`
	Game    string            `json:"game"`
	Kind    string            `json:"kind"`
	Native  string            `json:"native"`
	Account string            `json:"account"`
	Data    map[string]string `json:"data,omitempty"`
}

type bridgeState struct {
	mu      sync.Mutex
	hosts   map[string]bridgedHost // by account
	seq     int64
	signals []signalEvent
}

const signalFeedMax = 200

// bridgedXML adds the bridged presence of every account in ids that has no data of its own.
func (s *Server) bridgedXML(data map[string]string, ids []string) map[string]string {
	s.bridge.mu.Lock()
	defer s.bridge.mu.Unlock()
	now := time.Now()
	for _, id := range ids {
		if h, ok := s.bridge.hosts[id]; ok && now.Before(h.until) && data[id] == "" {
			if data == nil {
				data = map[string]string{}
			}
			data[id] = h.xml
		}
	}
	return data
}

// bridgedByJID finds a live bridged host a stanza is addressed to.
func (s *Server) bridgedByJID(to string) (bridgedHost, bool) {
	s.bridge.mu.Lock()
	defer s.bridge.mu.Unlock()
	bare := strings.SplitN(to, "/", 2)[0]
	for _, h := range s.bridge.hosts {
		if time.Now().Before(h.until) && (bare == h.jid || to == h.online || strings.HasPrefix(to, h.online+"@")) {
			return h, true
		}
	}
	return bridgedHost{}, false
}

// recordSignal keeps and logs, in full, a signaling stanza addressed to a bridged host.
func (s *Server) recordSignal(from *client, n node, to string) {
	h, ok := s.bridgedByJID(to)
	if !ok {
		return
	}
	e := signalEvent{Game: h.game, Kind: "signal", Native: from.id.OnlineID, Account: from.id.AccountID,
		Data: map[string]string{"to": to, "from": from.jid, "type": n.attr("type"), "id": n.attr("id"), "stanza": encoded(n)}}
	s.bridge.mu.Lock()
	s.bridge.seq++
	e.Seq, e.Time = s.bridge.seq, time.Now().UTC()
	s.bridge.signals = append(s.bridge.signals, e)
	if over := len(s.bridge.signals) - signalFeedMax; over > 0 {
		s.bridge.signals = s.bridge.signals[over:]
	}
	s.bridge.mu.Unlock()
	s.Log.Info("NP signaling toward a bridged host", "seq", e.Seq, "game", e.Game, "from", e.Native, "to", to, "type", e.Data["type"], "stanza", e.Data["stanza"])
}

func bridgeAuthed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := os.Getenv("NP_BRIDGE_KEY")
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if key == "" || !ok || subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// wellFormed reports whether s is balanced XML (the raw children a caller sends are relayed to consoles as they are).
func wellFormed(s string) bool {
	d := xml.NewDecoder(strings.NewReader(s))
	for {
		_, err := d.Token()
		if err != nil {
			return err.Error() == "EOF"
		}
	}
}

// RegisterBridge mounts the bridge routes.
func (s *Server) RegisterBridge(mux *http.ServeMux) {
	mux.HandleFunc("PUT /internal/bridge/presence/{account}", bridgeAuthed(s.bridgePresencePut))
	mux.HandleFunc("DELETE /internal/bridge/presence/{account}", bridgeAuthed(func(w http.ResponseWriter, r *http.Request) {
		s.bridge.mu.Lock()
		delete(s.bridge.hosts, strings.ToLower(r.PathValue("account")))
		s.bridge.mu.Unlock()
		w.Write([]byte("ok\n"))
	}))
	mux.HandleFunc("GET /internal/bridge/signaling", bridgeAuthed(func(w http.ResponseWriter, r *http.Request) {
		after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
		out := []signalEvent{}
		s.bridge.mu.Lock()
		for _, e := range s.bridge.signals {
			if e.Seq > after {
				out = append(out, e)
			}
		}
		s.bridge.mu.Unlock()
		json.NewEncoder(w).Encode(out)
	}))
}

func (s *Server) bridgePresencePut(w http.ResponseWriter, r *http.Request) {
	account := strings.ToLower(r.PathValue("account"))
	var q struct {
		Game     string `json:"game"`
		Title    string `json:"title"`
		Data     string `json:"data"`
		Children string `json:"children"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384)).Decode(&q); err != nil || !accountRe.MatchString(account) {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	body := q.Children
	if body == "" {
		blob, err := hex.DecodeString(q.Data)
		if err != nil || len(blob) == 0 || len(blob) > 1024 {
			http.Error(w, "want data (hex) or children (XML)", http.StatusBadRequest)
			return
		}
		body = `<x xmlns="` + bridgeDataNS + `" title=` + attr(q.Title) + `>` + hex.EncodeToString(blob) + `</x>`
	}
	if len(body) > maxPresenceData || !wellFormed(body) {
		http.Error(w, "presence children: well-formed XML up to 4 KiB", http.StatusBadRequest)
		return
	}
	svc := s.Service
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	acc, err := svc.Core.GetAccount(ctx, account)
	if err != nil {
		http.Error(w, "no such account", http.StatusNotFound)
		return
	}
	link, err := svc.EnsureLink(ctx, acc)
	if err != nil || link.OnlineID == "" {
		http.Error(w, "no NP identity", http.StatusNotFound)
		return
	}
	s.bridge.mu.Lock()
	if s.bridge.hosts == nil {
		s.bridge.hosts = map[string]bridgedHost{}
	}
	s.bridge.hosts[account] = bridgedHost{game: q.Game, xml: body, online: link.OnlineID, account: account, jid: community.JID(link.OnlineID, acc.Country), until: time.Now().Add(bridgeLease)}
	s.bridge.mu.Unlock()
	w.Write([]byte("ok\n"))
}
