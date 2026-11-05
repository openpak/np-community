// matching2-recorder accepts the PS3 Matching2 agent connections (plain TCP
// and RSA-kex TLS) and records every byte the console sends. It writes to a client only for commands
// reply.go implements, and only for titles listed in M2_PASSPHRASES. Run it only while capturing (research/ps3/matching2-protocol.md).
package main

import (
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"openpak/np-common/tlsfront"
)

var seq atomic.Int64

type recorder struct {
	dir string
	log *slog.Logger
	// pass maps a communication id to its 128-byte title passphrase (M2_PASSPHRASES). With an entry, the
	// recorder verifies each request's MAC and answers the commands in replies (reply.go); without one it
	// stays capture-only.
	pass map[string][]byte
	mu   sync.Mutex
	keys map[string][]byte // latest session MAC key per communication id (login on TLS, the rest on the plain connection)
}

func (r *recorder) setKey(id string, k []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.keys == nil {
		r.keys = map[string][]byte{}
	}
	r.keys[id] = k
}

func (r *recorder) getKey(id string) []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.keys[id]
}

func (r *recorder) handle(c net.Conn, kind string) {
	defer c.Close()
	remote := c.RemoteAddr().String()
	name := fmt.Sprintf("%s-%03d-%s-%s.log", time.Now().UTC().Format("20060102T150405Z"), seq.Add(1), kind, strings.NewReplacer(":", "_", "[", "", "]", "").Replace(remote))
	f, err := os.Create(filepath.Join(r.dir, name))
	if err != nil {
		r.log.Error("create log", "err", err)
		return
	}
	defer f.Close()
	var mu sync.Mutex
	note := func(format string, a ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(f, "%s %s\n", time.Now().UTC().Format(time.RFC3339Nano), fmt.Sprintf(format, a...))
	}
	note("open %s from %s", kind, remote)
	r.log.Info("connection", "kind", kind, "remote", remote, "file", name)
	if tc, ok := c.(*tls.Conn); ok {
		if err := tc.Handshake(); err != nil {
			note("handshake failed: %v", err)
			r.log.Warn("handshake failed", "remote", remote, "err", err)
			return
		}
		st := tc.ConnectionState()
		note("handshake ok: version=0x%04x suite=%s sni=%q", st.Version, tls.CipherSuiteName(st.CipherSuite), st.ServerName)
		r.log.Info("handshake ok", "remote", remote, "suite", tls.CipherSuiteName(st.CipherSuite), "sni", st.ServerName)
	}
	buf := make([]byte, 0x6000)
	st := &connState{}
	for {
		n, err := c.Read(buf)
		if n > 0 {
			mu.Lock()
			io.WriteString(f, dumpChunk(time.Now().UTC().Format(time.RFC3339Nano), buf[:n]))
			mu.Unlock()
			for _, out := range r.answer(buf[:n], st, note) {
				if _, werr := c.Write(out); werr != nil {
					note("write: %v", werr)
					break
				}
				mu.Lock()
				io.WriteString(f, dumpSent(time.Now().UTC().Format(time.RFC3339Nano), out))
				mu.Unlock()
			}
		}
		if err != nil {
			note("close: %v", err)
			return
		}
	}
}

func serve(ln net.Listener, kind string, r *recorder) {
	for {
		c, err := ln.Accept()
		if err != nil {
			r.log.Error("accept", "kind", kind, "err", err)
			return
		}
		go r.handle(c, kind)
	}
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func main() {
	log := slog.Default()
	host := os.Getenv("M2_HOST")
	if host == "" {
		log.Error("M2_HOST (agent hostname, e.g. matching.np.community.playstation.net) is required")
		os.Exit(1)
	}
	front, err := tlsfront.Load(env("M2_CA_CERT", "/keys/ps3-ca.pem"), env("M2_CA_KEY", "/keys/ps3-ca.key"))
	if err != nil {
		log.Error("load CA", "err", err)
		os.Exit(1)
	}
	front.Fallback = host
	dir := env("M2_LOG_DIR", "/captures")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Error("log dir", "err", err)
		os.Exit(1)
	}
	pass, err := parsePassphrases(os.Getenv("M2_PASSPHRASES"))
	if err != nil {
		log.Error("M2_PASSPHRASES", "err", err)
		os.Exit(1)
	}
	r := &recorder{dir: dir, log: log, pass: pass}
	plain, err := net.Listen("tcp", env("M2_PLAIN_ADDR", ":3480"))
	if err != nil {
		log.Error("listen plain", "err", err)
		os.Exit(1)
	}
	tl, err := net.Listen("tcp", env("M2_TLS_ADDR", ":8443"))
	if err != nil {
		log.Error("listen tls", "err", err)
		os.Exit(1)
	}
	log.Info("recording", "host", host, "plain", plain.Addr().String(), "tls", tl.Addr().String(), "dir", dir)
	go serve(plain, "plain", r)
	serve(tls.NewListener(tl, front.Config()), "tls", r)
}
