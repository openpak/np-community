package community

import (
	"context"
	"fmt"
	"log/slog"
	"openpak/np-common/coreclient"
	"time"
)

type Directory interface {
	AccountPage(context.Context, string) ([]*coreclient.Account, string, error)
}

// ProvisionAll is an idempotent existing-account migration. A full scan every
// minute covers new accounts without PlayStation logic in core registration.
func (s *Service) ProvisionAll(ctx context.Context, d Directory) (int, error) {
	count := 0
	after := ""
	for {
		accounts, next, e := d.AccountPage(ctx, after)
		if e != nil {
			return count, e
		}
		for _, a := range accounts {
			if _, e = s.EnsureLink(ctx, a); e != nil {
				return count, e
			}
			count++
		}
		if next == "" {
			return count, nil
		}
		if next <= after {
			return count, fmt.Errorf("account directory did not advance")
		}
		after = next
	}
}
func (s *Service) Reconcile(ctx context.Context, d Directory, log *slog.Logger) {
	for {
		n, e := s.ProvisionAll(ctx, d)
		if e != nil {
			log.Error("NP identity reconciliation failed", "processed", n, "err", e)
		} else {
			log.Info("NP identities reconciled", "accounts", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Minute):
		}
	}
}
