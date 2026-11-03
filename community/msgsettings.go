package community

import (
	"context"
	"encoding/base64"
	"encoding/xml"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Message settings: friend-list-update.../set_message_setting and friend-list-view.../get_message_setting.
//
// Wire shape from static RE of VSH 4.93 (research note ps3/messaging-protocol.md section 8):
//
//	request  <message-setting platform="ps3" sv=".." env=".."><ticket>b64</ticket><setting type="N">V</setting>...</message-setting>
//	         (get sends the same <setting type="N"/> elements without text)
//	reply    <message-setting result="00"><setting type="N">V</setting>...</message-setting>
//
// type and V are decimal integers. What each type number means is not in the firmware code
// that sends it, so the store keeps them opaque and nothing here changes message routing:
// messages already reach accepted friends only, which is the default policy.
const (
	maxSettingsPerRequest = 32
	resultOK              = "00"
	resultBad             = "ff"
)

// SettingsStore keeps each account's message settings as opaque type -> value pairs.
type SettingsStore interface {
	Set(ctx context.Context, account string, values map[int]int64) error
	// Get returns the stored value for every requested type that has one.
	Get(ctx context.Context, account string, types []int) (map[int]int64, error)
}

type MemorySettings struct {
	mu sync.Mutex
	m  map[string]map[int]int64
}

func (s *MemorySettings) Set(_ context.Context, account string, values map[int]int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]map[int]int64{}
	}
	if s.m[account] == nil {
		s.m[account] = map[int]int64{}
	}
	for k, v := range values {
		s.m[account][k] = v
	}
	return nil
}

func (s *MemorySettings) Get(_ context.Context, account string, types []int) (map[int]int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[int]int64{}
	for _, t := range types {
		if v, ok := s.m[account][t]; ok {
			out[t] = v
		}
	}
	return out, nil
}

type PGSettings struct{ DB *pgxpool.Pool }

func (s *PGSettings) Migrate(ctx context.Context) error {
	_, err := s.DB.Exec(ctx, `
	CREATE TABLE IF NOT EXISTS np_message_settings (
		acct  text   NOT NULL,
		type  int    NOT NULL,
		value bigint NOT NULL,
		PRIMARY KEY (acct, type))`)
	return err
}

func (s *PGSettings) Set(ctx context.Context, account string, values map[int]int64) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for k, v := range values {
		if _, err = tx.Exec(ctx, `INSERT INTO np_message_settings (acct, type, value) VALUES ($1,$2,$3)
			ON CONFLICT (acct, type) DO UPDATE SET value = EXCLUDED.value`, account, k, v); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PGSettings) Get(ctx context.Context, account string, types []int) (map[int]int64, error) {
	rows, err := s.DB.Query(ctx, `SELECT type, value FROM np_message_settings WHERE acct = $1 AND type = ANY($2)`, account, types)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]int64{}
	for rows.Next() {
		var t int
		var v int64
		if err := rows.Scan(&t, &v); err != nil {
			return nil, err
		}
		out[t] = v
	}
	return out, rows.Err()
}

type settingXML struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type settingsRequest struct {
	XMLName  xml.Name     `xml:"message-setting"`
	Ticket   string       `xml:"ticket"`
	Settings []settingXML `xml:"setting"`
}

func settingsReply(w http.ResponseWriter, result, inner string) {
	b := []byte(xml.Header + `<message-setting result="` + result + `">` + inner + `</message-setting>`)
	w.Header().Set("Content-Type", "text/xml; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(b)))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(b)
}

func (s *Service) messageSetting(set bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req settingsRequest
		d := xml.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10))
		if d.Decode(&req) != nil {
			http.Error(w, "invalid request", 400)
			return
		}
		var extra any
		if d.Decode(&extra) != io.EOF {
			http.Error(w, "invalid request", 400)
			return
		}
		raw, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(req.Ticket), ""))
		if err != nil {
			http.Error(w, "invalid ticket", 401)
			return
		}
		id, err := s.Verify(r.Context(), raw)
		if err != nil {
			http.Error(w, "invalid session", 401)
			return
		}
		if s.Settings == nil {
			settingsReply(w, resultBad, "")
			return
		}
		if len(req.Settings) == 0 || len(req.Settings) > maxSettingsPerRequest {
			settingsReply(w, resultBad, "")
			return
		}
		values := map[int]int64{}
		var types []int
		for _, st := range req.Settings {
			t, err := strconv.ParseInt(strings.TrimSpace(st.Type), 10, 32)
			if err != nil || t < 0 {
				settingsReply(w, resultBad, "")
				return
			}
			if set {
				v, err := strconv.ParseInt(strings.TrimSpace(st.Value), 10, 64)
				if err != nil {
					settingsReply(w, resultBad, "")
					return
				}
				values[int(t)] = v
			}
			types = append(types, int(t))
		}
		if set {
			if err := s.Settings.Set(r.Context(), id.AccountID, values); err != nil {
				http.Error(w, "settings unavailable", 503)
				return
			}
			settingsReply(w, resultOK, "")
			return
		}
		got, err := s.Settings.Get(r.Context(), id.AccountID, types)
		if err != nil {
			http.Error(w, "settings unavailable", 503)
			return
		}
		keys := make([]int, 0, len(got))
		for k := range got {
			keys = append(keys, k)
		}
		sort.Ints(keys)
		var b strings.Builder
		for _, k := range keys {
			b.WriteString(`<setting type="` + strconv.Itoa(k) + `">` + strconv.FormatInt(got[k], 10) + `</setting>`)
		}
		settingsReply(w, resultOK, b.String())
	}
}
