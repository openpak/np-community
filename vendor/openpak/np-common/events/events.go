// Package events follows the account core's invalidation stream so a ban or
// a deletion takes effect on open PlayStation sessions at once, not at their
// next core re-read.
//
//	account_banned          deny the account and cut its open sessions
//	account_unbanned        lift that ban
//	account_delete_started  suspend: deny and cut, for the core's grace period
//	account_restored        lift the suspension
//	account_deleted         purge: cut, and drop everything held for the account
//
// np-account keeps no database: the online ID is the subject of the account's
// core np link, which the core itself retires at final erasure
// (docs/handles.md), and every token this service signs is re-checked against
// the core on use. What this package holds is in memory: the denied accounts
// and the cursor. A start replays the stream from the beginning, which
// rebuilds the same set (each event's effect is a state, so replaying it is a
// no-op), and an event at or below the cursor is never applied twice.
package events

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"openpak/np-common/coreclient"
)

// Action is what one event does here.
type Action int

const (
	None Action = iota
	Ban
	Unban
	Suspend
	Restore
	Purge
)

// Plan maps a core event to its action. Events without an account, and every
// type that is not about the account's standing, do nothing.
func Plan(e coreclient.Event) Action {
	if e.AccountID == "" {
		return None
	}
	switch e.Type {
	case "account_banned":
		return Ban
	case "account_unbanned":
		return Unban
	case "account_delete_started":
		return Suspend
	case "account_restored":
		return Restore
	case "account_deleted":
		return Purge
	}
	return None
}

type standing struct{ banned, closing bool }

// State is the denied accounts and the stream cursor.
type State struct {
	mu     sync.Mutex
	cursor uint64
	acct   map[string]standing
}

func NewState() *State { return &State{acct: map[string]standing{}} }

// Denied reports whether the account is banned or closing as far as the
// stream has told this process.
func (s *State) Denied(account string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.acct[account]
	return ok
}

// Cursor is the version of the last applied event.
func (s *State) Cursor() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursor
}

// Apply records one event and reports its action, or None when the event is
// at or below the cursor (a redelivery). Out-of-order versions are dropped
// the same way.
func (s *State) Apply(e coreclient.Event) Action {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e.Version <= s.cursor {
		return None
	}
	s.cursor = e.Version
	a := Plan(e)
	st := s.acct[e.AccountID]
	switch a {
	case Ban:
		st.banned = true
	case Unban:
		st.banned = false
	case Suspend:
		st.closing = true
	case Restore:
		st.closing = false
	case Purge:
		delete(s.acct, e.AccountID)
		return a
	default:
		return a
	}
	if st.banned || st.closing {
		s.acct[e.AccountID] = st
	} else {
		delete(s.acct, e.AccountID)
	}
	return a
}

// Poller is the core's event poll.
type Poller interface {
	PollEvents(ctx context.Context, sinceVersion uint64, limit int) ([]coreclient.Event, error)
}

// Consumer applies the stream to State and cuts the sessions of an account
// that is denied or purged.
type Consumer struct {
	Core  Poller
	State *State
	// Cut ends every open session of the account (the XMPP streams); nil
	// when the community surface is off.
	Cut func(account string) int
	Log *slog.Logger
}

const batch = 1000

// ProcessOnce applies one page and returns how many events it held.
func (c *Consumer) ProcessOnce(ctx context.Context) (int, error) {
	evs, err := c.Core.PollEvents(ctx, c.State.Cursor(), batch)
	if err != nil {
		return 0, err
	}
	for _, e := range evs {
		switch c.State.Apply(e) {
		case Ban, Suspend, Purge:
			if c.Cut != nil {
				if n := c.Cut(e.AccountID); n > 0 && c.Log != nil {
					c.Log.Info("np events: sessions cut", "type", e.Type, "version", e.Version, "streams", n)
				}
			}
		}
	}
	return len(evs), nil
}

// Run polls until ctx ends: full pages back to back, otherwise every interval.
// Errors are logged without event content and retried.
func (c *Consumer) Run(ctx context.Context, interval time.Duration) {
	for ctx.Err() == nil {
		n, err := c.ProcessOnce(ctx)
		if err != nil && c.Log != nil {
			c.Log.Warn("np events: poll failed", "error", err)
		}
		if err == nil && n == batch {
			continue
		}
		select {
		case <-ctx.Done():
		case <-time.After(interval):
		}
	}
}

// Gate is the core as the sign-in and community paths see it: an account the
// stream has denied reads as disabled before the core is asked. The core
// refuses it too; this keeps the two from disagreeing while a cut is under way.
type Gate struct {
	*coreclient.Client
	State *State
}

func (g Gate) GetAccount(ctx context.Context, id string) (*coreclient.Account, error) {
	if g.State.Denied(id) {
		return nil, coreclient.ErrAccountDisabled
	}
	return g.Client.GetAccount(ctx, id)
}
