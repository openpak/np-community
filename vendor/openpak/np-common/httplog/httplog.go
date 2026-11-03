// Package httplog writes one access line per request to the standard logger (stderr):
//
//	http <method> <path> -> <status> <duration>ms
//
// Only the method and URL path are logged. Never the query string, headers, cookies, tokens,
// passwords, certificates, keys or bodies, which carry credentials on the auth paths.
package httplog

import (
	"bufio"
	"errors"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Option adjusts Wrap.
type Option func(*config)

type config struct {
	extra func(http.ResponseWriter) string
}

// WithExtra appends the returned text (already safe to log, e.g. "k=v") to each line. It is called after
// the handler has returned, with the response writer, so it can read response headers.
func WithExtra(f func(http.ResponseWriter) string) Option { return func(c *config) { c.extra = f } }

// Wrap logs every request served by next except health checks (/healthz).
func Wrap(next http.Handler, opts ...Option) http.Handler {
	var c config
	for _, o := range opts {
		o(&c)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		line := "http " + r.Method + " " + safe(r.URL.Path) + " -> " + strconv.Itoa(sw.status) + " " + ms(time.Since(start))
		if c.extra != nil {
			if e := c.extra(w); e != "" {
				line += " " + e
			}
		}
		log.Print(line)
	})
}

// Command logs one line for a non-HTTP command or message: "cmd <name> -> <result> <duration>ms".
// name and result must be short fixed labels, never request data.
func Command(name, result string, start time.Time) {
	log.Print("cmd " + safe(name) + " -> " + safe(result) + " " + ms(time.Since(start)))
}

func ms(d time.Duration) string { return strconv.FormatInt(d.Milliseconds(), 10) + "ms" }

// safe keeps a hostile path from forging log lines.
func safe(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] <= ' ' || s[i] == 0x7f {
			return strconv.Quote(s)
		}
	}
	return s
}

type statusWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (w *statusWriter) WriteHeader(code int) {
	// 1xx informational responses are not the final status.
	if !w.wroteHeader && code >= 200 {
		w.status = code
		w.wroteHeader = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wroteHeader = true
	return w.ResponseWriter.Write(b)
}

// Flush keeps streaming responses working.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Hijack keeps connection upgrades working.
func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if h, ok := w.ResponseWriter.(http.Hijacker); ok {
		return h.Hijack()
	}
	return nil, nil, errors.New("httplog: underlying writer is not a Hijacker")
}

// Unwrap lets http.ResponseController reach the real writer (deadlines, HTTP/2 push).
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
