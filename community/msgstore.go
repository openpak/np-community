package community

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Stored-message limits. The firmware knows a "max_exceed" error for a full queue; the
// numbers are OpenPak's choice, not recovered from the firmware.
const (
	MaxStoredPerRecipient = 100
	StoredTTL             = 30 * 24 * time.Hour
)

// ErrMailboxFull is returned by MessageStore.Put when the recipient has MaxStoredPerRecipient waiting.
var ErrMailboxFull = errors.New("mailbox full")

// StoredMessage is one offline message. XML is the complete forwarded <message> stanza
// without the <x xmlns=".../stored"> marker, which is added at delivery.
type StoredMessage struct {
	ID      int64
	To      string // recipient core account id
	XML     string
	Stamp   time.Time
	Expires time.Time
}

// MessageStore keeps messages for recipients that are not connected.
type MessageStore interface {
	Put(ctx context.Context, to, xml string, now time.Time) (StoredMessage, error)
	// Pending returns the recipient's unexpired messages, oldest first.
	Pending(ctx context.Context, to string, now time.Time) ([]StoredMessage, error)
	Delete(ctx context.Context, to string, id int64) error
}

// MemoryStore is the non-durable store used when no database is configured, and in tests.
type MemoryStore struct {
	mu   sync.Mutex
	next int64
	byTo map[string][]StoredMessage
}

func (m *MemoryStore) Put(_ context.Context, to, xml string, now time.Time) (StoredMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.byTo == nil {
		m.byTo = map[string][]StoredMessage{}
	}
	live := m.byTo[to][:0]
	for _, s := range m.byTo[to] {
		if s.Expires.After(now) {
			live = append(live, s)
		}
	}
	m.byTo[to] = live
	if len(live) >= MaxStoredPerRecipient {
		return StoredMessage{}, ErrMailboxFull
	}
	m.next++
	s := StoredMessage{ID: m.next, To: to, XML: xml, Stamp: now, Expires: now.Add(StoredTTL)}
	m.byTo[to] = append(m.byTo[to], s)
	return s, nil
}

func (m *MemoryStore) Pending(_ context.Context, to string, now time.Time) ([]StoredMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []StoredMessage
	for _, s := range m.byTo[to] {
		if s.Expires.After(now) {
			out = append(out, s)
		}
	}
	return out, nil
}

func (m *MemoryStore) Delete(_ context.Context, to string, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.byTo[to]
	for i, s := range list {
		if s.ID == id {
			m.byTo[to] = append(list[:i:i], list[i+1:]...)
			break
		}
	}
	return nil
}

// PGStore is the Postgres MessageStore.
type PGStore struct{ DB *pgxpool.Pool }

func (s *PGStore) Migrate(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `
	CREATE TABLE IF NOT EXISTS np_messages (
		id      bigserial   PRIMARY KEY,
		to_acct text        NOT NULL,
		xml     text        NOT NULL,
		stamp   timestamptz NOT NULL,
		expires timestamptz NOT NULL);
	CREATE INDEX IF NOT EXISTS np_messages_to ON np_messages (to_acct, id)`)
	return err
}

func (s *PGStore) Put(ctx context.Context, to, xml string, now time.Time) (StoredMessage, error) {
	tx, err := s.DB.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return StoredMessage{}, err
	}
	defer tx.Rollback(ctx)
	// Serialise per recipient so concurrent senders cannot overshoot the cap.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, to); err != nil {
		return StoredMessage{}, err
	}
	if _, err = tx.Exec(ctx, `DELETE FROM np_messages WHERE to_acct = $1 AND expires <= $2`, to, now); err != nil {
		return StoredMessage{}, err
	}
	var n int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM np_messages WHERE to_acct = $1`, to).Scan(&n); err != nil {
		return StoredMessage{}, err
	}
	if n >= MaxStoredPerRecipient {
		return StoredMessage{}, ErrMailboxFull
	}
	m := StoredMessage{To: to, XML: xml, Stamp: now, Expires: now.Add(StoredTTL)}
	if err = tx.QueryRow(ctx, `INSERT INTO np_messages (to_acct, xml, stamp, expires) VALUES ($1,$2,$3,$4) RETURNING id`,
		to, xml, m.Stamp, m.Expires).Scan(&m.ID); err != nil {
		return StoredMessage{}, err
	}
	return m, tx.Commit(ctx)
}

func (s *PGStore) Pending(ctx context.Context, to string, now time.Time) ([]StoredMessage, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, xml, stamp, expires FROM np_messages
		WHERE to_acct = $1 AND expires > $2 ORDER BY id`, to, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredMessage
	for rows.Next() {
		m := StoredMessage{To: to}
		if err := rows.Scan(&m.ID, &m.XML, &m.Stamp, &m.Expires); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *PGStore) Delete(ctx context.Context, to string, id int64) error {
	_, err := s.DB.Exec(ctx, `DELETE FROM np_messages WHERE to_acct = $1 AND id = $2`, to, id)
	return err
}
