package xmpp

// The PS3 half of the Minecraft cross-play bridge (card openpak/prds#177). A player hosting a game on another console is not signed in to
// this relay, but their PS3 friends must see the game, and a PS3 that tries to reach the host sends NP signaling toward a JID nobody holds.
// common/minecraft's session directory uses three routes (bearer NP_BRIDGE_KEY):
//
//	PUT    /internal/bridge/presence/{account}  {game, title, data (hex, the 56-byte presence blob; optional product, name, text) or children
//	                                           (raw XML)}: the host's game presence, shown to every friend who sees the account online, for
//	                                           90 s per PUT; viewers already online get it at once when it changes
//	DELETE /internal/bridge/presence/{account}  take it back
//	GET    /internal/bridge/signaling?after=N   the signaling stanzas addressed to a bridged host, in full
//
// Bridged presence lives in memory with a lease (the directory renews it every 30 s), apart from the presence data a real console stores in
// Postgres. A real console signed in on the same account wins only while it is itself hosting (its presence carries an ext element, the game
// data); its plain sign-in presence (mode=game, no data) does not hide the bridged game.

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
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

// Namespaces and values of a real PS3 Minecraft hosting presence. Proof: the 2026-11-11 stanzas np-community relayed from the owner's real PS3
// (scratch/ps3-signaling-2026-11-09/mc1.log, mc2.log: stanzatype, ext with the blob inline as base64, presence mode=game with objectId,
// localName, localLang, gameSpecified) and the sign-in presence logged at 15:55:04 (discovery-test-1111.log). GUESSED: the product id of
// other regions (only the EU NPEB01899_00 is known), the gameSpecified text (the real one read "Playing Alone - Enjoying the view!" with a lone
// host; what it says with several players is unknown), localLang (18 in both captures).
const (
	nsStanza  = "http://playstation.net/np/rt/stanzatype"
	nsExt     = "http://playstation.net/np/rt/ext"
	nsPresent = "http://playstation.net/np/rt/presence"

	defProduct = "NPEB01899_00"
	defName    = "Minecraft: PlayStation®3 Edition"
	defText    = "Playing Alone - Enjoying the view!"
	defLang    = "18"
)

// hostingChildren is the presence a real console sets while hosting, around the game's blob, in the form the relay stores and forwards
// (the children of <presence> except profile and status, each element repeating its namespace).
func hostingChildren(comm string, blob []byte, product, name, text string) string {
	if product == "" {
		product = defProduct
	}
	if name == "" {
		name = defName
	}
	if text == "" {
		text = defText
	}
	el := func(ns, local, body string) string {
		return `<` + local + ` xmlns=` + attr(ns) + `>` + escaped(body) + `</` + local + `>`
	}
	return `<x xmlns=` + attr(nsStanza) + `></x>` +
		`<x xmlns=` + attr(nsExt) + ` context=` + attr(comm) + `>` + el(nsExt, "origin", comm) +
		`<data xmlns=` + attr(nsExt) + ` inline="base64">` + base64.StdEncoding.EncodeToString(blob) + `</data></x>` +
		`<x xmlns=` + attr(nsPresent) + `>` + el(nsPresent, "mode", "game") + el(nsPresent, "objectId", product) + el(nsPresent, "localName", name) +
		el(nsPresent, "localLang", defLang) + el(nsPresent, "gameSpecified", text) + `</x>`
}

// hasGameData reports whether a stored real presence carries the game's data blob (a real console that is hosting).
func hasGameData(xml string) bool { return strings.Contains(xml, `xmlns="`+nsExt+`"`) }

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
		if h, ok := s.bridge.hosts[id]; ok && now.Before(h.until) && !hasGameData(data[id]) {
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
		acct := strings.ToLower(r.PathValue("account"))
		s.bridge.mu.Lock()
		_, had := s.bridge.hosts[acct]
		delete(s.bridge.hosts, acct)
		s.bridge.mu.Unlock()
		if had {
			s.pushAll()
		}
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
		Product  string `json:"product"` // objectId of the game's presence (default: the EU NPEB01899_00)
		Name     string `json:"name"`
		Text     string `json:"text"` // gameSpecified
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
		body = hostingChildren(q.Title, blob, q.Product, q.Name, q.Text)
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
	changed := s.bridge.hosts[account].xml != body
	s.bridge.hosts[account] = bridgedHost{game: q.Game, xml: body, online: link.OnlineID, account: account, jid: community.JID(link.OnlineID, acc.Country), until: time.Now().Add(bridgeLease)}
	s.bridge.mu.Unlock()
	if changed {
		s.pushAll() // viewers already online see the game now, not at their next poll
	}
	w.Write([]byte("ok\n"))
}

// pushAll refreshes every signed-in stream now (each one resends only what changed for it).
func (s *Server) pushAll() {
	s.mu.Lock()
	cs := make([]*client, 0, len(s.clients))
	for c := range s.clients {
		cs = append(cs, c)
	}
	s.mu.Unlock()
	for _, c := range cs {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if e := c.refresh(ctx); e != nil {
				s.Log.Warn("NP bridged presence push failed", "online_id", c.id.OnlineID, "error", e)
			}
		}()
	}
}
