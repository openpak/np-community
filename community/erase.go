package community

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
)

// Eraser is a store that can drop everything it holds for one account.
type Eraser interface {
	Erase(ctx context.Context, account string) (int, error)
}

// Erase removes every row this service keeps for the account: the chat delivery cursor, the
// message settings, and the PS3-only message extensions the account sent or received. It is
// idempotent. Chat messages themselves belong to the chat service.
func (s *Service) Erase(ctx context.Context, account string) (int, error) {
	total := 0
	for _, st := range []any{s.Inbox, s.Settings} {
		e, ok := st.(Eraser)
		if !ok {
			continue
		}
		n, err := e.Erase(ctx, account)
		total += n
		if err != nil {
			return total, err
		}
	}
	slog.Info("account erased", "account", account, "rows", total, "service", "np-community")
	return total, nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// EraseHandler serves DELETE /internal/accounts/{account_id}/data, guarded by a bearer key.
// An empty key answers 503: the endpoint is never open.
func (s *Service) EraseHandler(key string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /internal/accounts/{account_id}/data", func(w http.ResponseWriter, r *http.Request) {
		if key == "" {
			http.Error(w, "erase disabled", http.StatusServiceUnavailable)
			return
		}
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		id := r.PathValue("account_id")
		if !uuidPattern.MatchString(id) {
			http.Error(w, "invalid account id", http.StatusBadRequest)
			return
		}
		n, err := s.Erase(r.Context(), strings.ToLower(id))
		if err != nil {
			slog.Warn("account erase failed", "service", "np-community", "error", err)
			http.Error(w, "erase failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"service": "np-community", "rows": n})
	})
	return mux
}
