package nphttp

import (
	"log/slog"
	"net/http"
)

// Logging keeps firmware failures observable without logging credentials or bodies.
func Logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		response := &responseStatus{ResponseWriter: w, status: 200}
		next.ServeHTTP(response, r)
		slog.Info("NP HTTP", "method", r.Method, "host", r.Host, "path", r.URL.Path, "status", response.status, "ps3_status", w.Header().Get("X-I-5-Status"))
	})
}

type responseStatus struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *responseStatus) WriteHeader(status int) {
	if !w.wroteHeader && status >= 200 {
		w.status = status
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *responseStatus) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}
func (w *responseStatus) Unwrap() http.ResponseWriter { return w.ResponseWriter }
