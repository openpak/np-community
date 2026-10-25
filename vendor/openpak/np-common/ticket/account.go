package ticket

import (
	"context"
	"openpak/np-common/coreclient"
)

type AccountCore interface {
	GetAccount(context.Context, string) (*coreclient.Account, error)
	GetLinkByAccount(context.Context, string) (*coreclient.Link, error)
}

// VerifyAccount authenticates the ticket and rechecks current account standing and link ownership.
func VerifyAccount(ctx context.Context, core AccountCore, signer *Signer, raw []byte) (Identity, error) {
	id, err := signer.Verify(raw)
	if err != nil {
		return id, err
	}
	if _, err = core.GetAccount(ctx, id.AccountID); err != nil {
		return Identity{}, err
	}
	link, err := core.GetLinkByAccount(ctx, id.AccountID)
	if err != nil {
		return Identity{}, err
	}
	if link.LinkID != id.LinkID || link.OnlineID != id.OnlineID {
		return Identity{}, ErrInvalid
	}
	return id, nil
}
