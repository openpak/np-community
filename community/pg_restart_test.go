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
}
