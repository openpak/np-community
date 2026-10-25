package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"openpak/np-common/coreclient"
	"openpak/np-common/events"
	"openpak/np-common/nphttp"
	"openpak/np-common/ticket"
	"openpak/np-community/community"
	"openpak/np-community/xmpp"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if err := run(); err != nil {
		slog.Error("np-community stopped", "err", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Getenv("NP_CORE_KEY")) < 32 {
		return fmt.Errorf("NP_CORE_KEY must be at least 32 bytes")
	}
	signer, err := ticket.Load(os.Getenv("NP_TICKET_KEY"))
	if err != nil {
		return err
	}
	core, err := coreclient.Dial(nphttp.Env("NP_CORE_ADDR", "localhost:7000"), os.Getenv("NP_CORE_KEY"))
	if err != nil {
		return err
	}
	defer core.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	standing := events.NewState()
	gate := events.Gate{Client: core, State: standing}
	svc := &community.Service{Core: gate, Tickets: signer, Client: nphttp.Env("NP_SESSION_CLIENT", "ps3"), OS: os.Getenv("NP_SESSION_OS")}
	if path := os.Getenv("NP_TROPHY_RESOURCE_KEY"); path != "" {
		svc.ResourceKey, err = os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(svc.ResourceKey) != 20 {
			return fmt.Errorf("NP_TROPHY_RESOURCE_KEY must contain 20 bytes")
		}
	}
	front, err := nphttp.Front()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", nphttp.Env("NP_XMPP_ADDR", ":8523"))
	if err != nil {
		return err
	}
	defer ln.Close()
	if os.Getenv("NP_XMPP_TLS") == "1" {
		front.Fallback = "us.us.np.communication.playstation.net"
		ln = tls.NewListener(ln, front.Config())
	}
	xs := &xmpp.Server{Service: svc, Log: slog.Default()}
	consumer := &events.Consumer{Core: core, State: standing, Log: slog.Default(), Cut: xs.Disconnect}
	go consumer.Run(ctx, 5*time.Second)
	if os.Getenv("NP_PROVISION_ACCOUNTS") != "false" {
		go svc.Reconcile(ctx, core, slog.Default())
	}
	errs := make(chan error, 1)
	go func() { errs <- xs.Serve(ctx, ln); stop() }()
	mux := http.NewServeMux()
	svc.Register(mux)
	nphttp.Health(mux)
	err = nphttp.Run(ctx, mux, nphttp.Env("NP_HTTP_ADDR", ":8003"), os.Getenv("NP_TLS_ADDR"), front)
	stop()
	ln.Close()
	select {
	case e := <-errs:
		if err == nil {
			err = e
		}
	case <-time.After(10 * time.Second):
		if err == nil {
			err = fmt.Errorf("XMPP shutdown timed out")
		}
	}
	return err
}
