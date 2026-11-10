package community

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Needs a scratch Postgres: NP_TEST_DATABASE_URL=postgres://...; skipped otherwise.
func TestPGStateSurvivesNewInstance(t *testing.T) {
	url := os.Getenv("NP_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("NP_TEST_DATABASE_URL unset")
	}
	ctx := context.Background()
	open := func() *PGInbox {
		p, err := pgxpool.New(ctx, url)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		in := &PGInbox{DB: p}
		if err := in.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
		return in
	}
	if err := open().Advance(ctx, "restart-test", 42); err != nil {
		t.Fatal(err)
	}
	v, ok, err := open().Cursor(ctx, "restart-test")
	if err != nil || !ok || v != 42 {
		t.Fatalf("cursor after restart = %d %v %v", v, ok, err)
	}
	// the presence data a console set survives a restart too, is replaced by the next one, and goes with the account on erasure
	if err := open().PutPresence(ctx, "restart-test", "<x>one</x>"); err != nil {
		t.Fatal(err)
	}
	in := open()
	if got, err := in.Presences(ctx, []string{"restart-test", "nobody"}); err != nil || len(got) != 1 || got["restart-test"] != "<x>one</x>" {
		t.Fatalf("presence after restart = %v %v", got, err)
	}
	in.PutPresence(ctx, "restart-test", "<x>two</x>")
	if got, _ := in.Presences(ctx, []string{"restart-test"}); got["restart-test"] != "<x>two</x>" {
		t.Fatalf("presence not replaced: %v", got)
	}
	if n, err := in.Erase(ctx, "restart-test"); err != nil || n < 2 {
		t.Fatalf("erase = %d %v", n, err)
	}
	if got, _ := in.Presences(ctx, []string{"restart-test"}); len(got) != 0 {
		t.Fatalf("presence survived erasure: %v", got)
	}
}
