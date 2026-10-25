package nphttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"openpak/np-common/tlsfront"
	"os"
	"time"
)

func Server(addr string, h http.Handler) *http.Server {
	return &http.Server{Addr: addr, Handler: Logging(h), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 60 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second, MaxHeaderBytes: 8 << 10}
}

// Run binds every listener before serving and shuts all listeners down together.
func Run(ctx context.Context, h http.Handler, addr, tlsAddr string, front *tlsfront.Front) error {
	servers := []*http.Server{Server(addr, h)}
	listeners := []net.Listener{}
	defer func() {
		for _, l := range listeners {
			l.Close()
		}
	}()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	listeners = append(listeners, ln)
	if tlsAddr != "" {
		if front == nil {
			return fmt.Errorf("TLS address requires a PS3 CA")
		}
		ln, err = net.Listen("tcp", tlsAddr)
		if err != nil {
			return err
		}
		listeners = append(listeners, ln)
		servers = append(servers, front.Serve(Server(tlsAddr, h)))
	}
	done := make(chan error, len(servers))
	for i, s := range servers {
		go func(i int, s *http.Server) {
			var e error
			if i == 0 {
				e = s.Serve(listeners[i])
			} else {
				e = s.ServeTLS(listeners[i], "", "")
			}
			done <- e
		}(i, s)
	}
	select {
	case <-ctx.Done():
		err = nil
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, s := range servers {
		if e := s.Shutdown(shutdown); e != nil {
			s.Close()
			if err == nil {
				err = e
			}
		}
	}
	return err
}
func Front() (*tlsfront.Front, error) {
	if os.Getenv("NP_TLS_ADDR") == "" && os.Getenv("NP_XMPP_TLS") != "1" {
		return nil, nil
	}
	return tlsfront.Load(os.Getenv("NP_TLS_CA_CERT"), os.Getenv("NP_TLS_CA_KEY"))
}
func Health(m *http.ServeMux) {
	m.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok\n"))
	})
}
func Env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
