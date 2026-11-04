package community

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	acctA = "11111111-1111-4111-8111-111111111111"
	acctB = "22222222-2222-4222-8222-222222222222"
)

func eraseFixture(t *testing.T) (*Service, *MemoryInbox, *MemorySettings) {
	ctx := t.Context()
	in, st := &MemoryInbox{}, &MemorySettings{}
	for _, a := range []string{acctA, acctB} {
		in.Advance(ctx, a, 5)
		st.Set(ctx, a, map[int]int64{1: 2, 3: 4})
	}
	in.PutExt(ctx, Ext{ChatID: 1, Sender: acctA, Recipient: acctB})
	in.PutExt(ctx, Ext{ChatID: 2, Sender: acctB, Recipient: acctA})
	in.PutExt(ctx, Ext{ChatID: 3, Sender: acctB, Recipient: acctB})
	return &Service{Inbox: in, Settings: st}, in, st
}

func TestEraseRemovesOnlyThatAccount(t *testing.T) {
	s, in, st := eraseFixture(t)
	n, err := s.Erase(t.Context(), acctA)
	if err != nil || n != 5 { // cursor, 2 settings, 2 ext
		t.Fatalf("n=%d err=%v", n, err)
	}
	if _, ok, _ := in.Cursor(t.Context(), acctA); ok {
		t.Fatal("cursor kept")
	}
	if _, ok, _ := in.Ext(t.Context(), 1); ok {
		t.Fatal("sent ext kept")
	}
	if _, ok, _ := in.Ext(t.Context(), 2); ok {
		t.Fatal("received ext kept")
	}
	if _, ok, _ := in.Ext(t.Context(), 3); !ok {
		t.Fatal("other account's ext removed")
	}
	if _, ok, _ := in.Cursor(t.Context(), acctB); !ok {
		t.Fatal("other cursor removed")
	}
	if got, _ := st.Get(t.Context(), acctB, []int{1, 3}); len(got) != 2 {
		t.Fatal("other settings removed")
	}
	if n, err := s.Erase(t.Context(), acctA); err != nil || n != 0 {
		t.Fatalf("second run n=%d err=%v", n, err)
	}
}

func TestEraseEndpoint(t *testing.T) {
	s, _, _ := eraseFixture(t)
	call := func(h http.Handler, id, auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("DELETE", "/internal/accounts/"+id+"/data", nil)
		if auth != "" {
			r.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	h := s.EraseHandler("secret-key")
	for _, c := range []struct {
		id, auth string
		code     int
	}{{acctA, "", 401}, {acctA, "Bearer nope", 401}, {"not-a-uuid", "Bearer secret-key", 400}} {
		if w := call(h, c.id, c.auth); w.Code != c.code {
			t.Fatalf("%+v -> %d", c, w.Code)
		}
	}
	if w := call(s.EraseHandler(""), acctA, "Bearer "); w.Code != 503 {
		t.Fatalf("unset key -> %d", w.Code)
	}
	w := call(h, acctA, "Bearer secret-key")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"service":"np-community"`) || !strings.Contains(w.Body.String(), `"rows":5`) {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}
