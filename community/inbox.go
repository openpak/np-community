package community

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Ext is what chat cannot carry for a message sent from a PS3: the original stanza's subject/body split,
// id, type, and its non-text children (np/rt/content attachments, message_kind, ...) as XML. It is
// delivered again only to PS3 recipients; every other network sees the text.
type Ext struct {
	ChatID         int64
	Subject, Body  string
	StanzaID, Type string
	XML            string // concatenated child elements other than subject, body and the stored marker
}

// InboxStore is the PS3 side's own state about chat: the per-account delivery cursor, the event
// follower's position in the core's stream, and the Ext rows.
type InboxStore interface {
	// Cursor is the highest chat message id delivered to the account; ok is false if none is recorded.
	Cursor(ctx context.Context, account string) (id int64, ok bool, err error)
	// Advance moves the cursor forward (never back), creating it if needed.
	Advance(ctx context.Context, account string, id int64) error
	// Version is the last core event version the follower applied.
	Version(ctx context.Context) (v uint64, ok bool, err error)
	SetVersion(ctx context.Context, v uint64) error
	PutExt(ctx context.Context, e Ext) error
	Ext(ctx context.Context, chatID int64) (Ext, bool, error)
}

// MemoryInbox is the non-durable InboxStore for tests and for running without a database.
type MemoryInbox struct {
	mu      sync.Mutex
	cursors map[string]int64
	version uint64
	hasVer  bool
	ext     map[int64]Ext
}

func (m *MemoryInbox) Cursor(_ context.Context, a string) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.cursors[a]
	return v, ok, nil
}
func (m *MemoryInbox) Advance(_ context.Context, a string, id int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cursors == nil {
		m.cursors = map[string]int64{}
	}
	if v, ok := m.cursors[a]; !ok || id > v {
		m.cursors[a] = id
	}
	return nil
}
func (m *MemoryInbox) Version(context.Context) (uint64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.version, m.hasVer, nil
}
func (m *MemoryInbox) SetVersion(_ context.Context, v uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.version, m.hasVer = v, true
	return nil
}
func (m *MemoryInbox) PutExt(_ context.Context, e Ext) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ext == nil {
		m.ext = map[int64]Ext{}
	}
	m.ext[e.ChatID] = e
	return nil
}
func (m *MemoryInbox) Ext(_ context.Context, id int64) (Ext, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.ext[id]
	return e, ok, nil
}

// PGInbox is the Postgres InboxStore.
type PGInbox struct{ DB *pgxpool.Pool }

// Migrate creates the tables and drops the retired np_messages queue (friend messages now live in chat).
func (s *PGInbox) Migrate(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `
	CREATE TABLE IF NOT EXISTS np_chat_cursor (
		account text PRIMARY KEY,
		last_id bigint NOT NULL);
	CREATE TABLE IF NOT EXISTS np_chat_follow (
		id      int PRIMARY KEY CHECK (id = 1),
		version bigint NOT NULL);
	CREATE TABLE IF NOT EXISTS np_message_ext (
		chat_id   bigint PRIMARY KEY,
		subject   text NOT NULL,
		body      text NOT NULL,
		stanza_id text NOT NULL,
		type      text NOT NULL,
		xml       text NOT NULL,
		created   timestamptz NOT NULL DEFAULT now());
	DELETE FROM np_message_ext WHERE created < now() - interval '35 days';
	DROP TABLE IF EXISTS np_messages`)
	return err
}

func (s *PGInbox) Cursor(ctx context.Context, a string) (int64, bool, error) {
	var id int64
	err := s.DB.QueryRow(ctx, `SELECT last_id FROM np_chat_cursor WHERE account = $1`, a).Scan(&id)
	if err == pgx.ErrNoRows {
		return 0, false, nil
	}
	return id, err == nil, err
}
func (s *PGInbox) Advance(ctx context.Context, a string, id int64) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO np_chat_cursor (account, last_id) VALUES ($1,$2)
		ON CONFLICT (account) DO UPDATE SET last_id = GREATEST(np_chat_cursor.last_id, EXCLUDED.last_id)`, a, id)
	return err
}
func (s *PGInbox) Version(ctx context.Context) (uint64, bool, error) {
	var v int64
	err := s.DB.QueryRow(ctx, `SELECT version FROM np_chat_follow WHERE id = 1`).Scan(&v)
	if err == pgx.ErrNoRows {
		return 0, false, nil
	}
	return uint64(v), err == nil, err
}
func (s *PGInbox) SetVersion(ctx context.Context, v uint64) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO np_chat_follow (id, version) VALUES (1,$1)
		ON CONFLICT (id) DO UPDATE SET version = EXCLUDED.version`, int64(v))
	return err
}
func (s *PGInbox) PutExt(ctx context.Context, e Ext) error {
	_, err := s.DB.Exec(ctx, `INSERT INTO np_message_ext (chat_id, subject, body, stanza_id, type, xml, created)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (chat_id) DO NOTHING`,
		e.ChatID, e.Subject, e.Body, e.StanzaID, e.Type, e.XML, time.Now().UTC())
	return err
}
func (s *PGInbox) Ext(ctx context.Context, id int64) (Ext, bool, error) {
	e := Ext{ChatID: id}
	err := s.DB.QueryRow(ctx, `SELECT subject, body, stanza_id, type, xml FROM np_message_ext WHERE chat_id = $1`, id).
		Scan(&e.Subject, &e.Body, &e.StanzaID, &e.Type, &e.XML)
	if err == pgx.ErrNoRows {
		return Ext{}, false, nil
	}
	return e, err == nil, err
}
