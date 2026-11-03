package nphttp

import (
	"net/http"

	"openpak/np-common/httplog"
)

// Logging writes one stderr line per request (method, path, status, duration), plus the PS3 status
// header when the handler set one. No query, headers, credentials or bodies; /healthz is skipped.
func Logging(next http.Handler) http.Handler {
	return httplog.Wrap(next, httplog.WithExtra(func(w http.ResponseWriter) string {
		if s := w.Header().Get("X-I-5-Status"); s != "" {
			return "ps3_status=" + s
		}
		return ""
	}))
}
