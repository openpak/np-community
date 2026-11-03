package community

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"openpak/np-common/coreclient"
	"openpak/np-common/fakecore"
	"openpak/np-common/identity"
	"openpak/np-common/ticket"
)

func TestMessageSettings(t *testing.T) {
	const key = "core-key-core-key-core-key-core-key"
	f := fakecore.New(key)
	f.AddAccount(fakecore.Account{ID: "alice", DisplayName: "Alice", Country: "PT"})
	f.AddAccount(fakecore.Account{ID: "bob", DisplayName: "Bobby", Country: "US"})
	core, err := coreclient.Dial("passthrough:///core", key, f.Serve(t)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { core.Close() })
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	signer, _ := ticket.New(priv, nil)
	s := &Service{Core: core, Tickets: signer, Settings: &MemorySettings{}}
	mux := http.NewServeMux()
	s.Register(mux)

	tk := func(acct string) string {
		a, _ := core.GetAccount(t.Context(), acct)
		l, err := identity.EnsureNPLink(t.Context(), core, a)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := signer.Issue(ticket.Identity{AccountID: l.AccountID, LinkID: l.LinkID, OnlineID: l.OnlineID, Country: "us", Service: "IV0001-NPXS01001_00"}, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		return base64.StdEncoding.EncodeToString(raw)
	}
	post := func(path, acct, inner string) (int, string) {
		body := `<?xml version="1.0" encoding="utf-8"?><message-setting platform="ps3" sv="4.93" env="np"><ticket>` + tk(acct) + `</ticket>` + inner + `</message-setting>`
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
		return w.Code, w.Body.String()
	}
	const set, get = "/friend_list_update/sec/set_message_setting", "/friend_list_view/sec/get_message_setting"

	// Nothing stored: ok, and no setting elements (the firmware keeps -1 for those).
	if c, r := post(get, "alice", `<setting type="1"/>`); c != 200 || !strings.Contains(r, `<message-setting result="00"></message-setting>`) {
		t.Fatal(c, r)
	}
	if c, r := post(set, "alice", `<setting type="1">2</setting><setting type="7">0</setting>`); c != 200 || !strings.Contains(r, `result="00"`) {
		t.Fatal(c, r)
	}
	r := func() string {
		_, r := post(get, "alice", `<setting type="7"/><setting type="1"/><setting type="9"/>`)
		return r
	}()
	if !strings.Contains(r, `<setting type="1">2</setting><setting type="7">0</setting></message-setting>`) {
		t.Fatal(r)
	}
	// Per account.
	if _, r := post(get, "bob", `<setting type="1"/>`); strings.Contains(r, "<setting") {
		t.Fatal(r)
	}
	// Malformed values and empty requests answer ff; a bad ticket is 401.
	for _, in := range []string{``, `<setting type="x">1</setting>`, `<setting type="1">y</setting>`} {
		if _, r := post(set, "alice", in); !strings.Contains(r, `result="ff"`) {
			t.Fatalf("%q: %s", in, r)
		}
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("POST", set, strings.NewReader(`<message-setting><ticket>AAAA</ticket><setting type="1">1</setting></message-setting>`)))
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
}
