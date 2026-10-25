package coreclient

import (
	"context"
	accountv1 "github.com/openpak/account-api/openpak/account/v1"
)

func (c *Client) AccountPage(ctx context.Context, after string) ([]*Account, string, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, e := c.ident.ListAccounts(ctx, &accountv1.ListAccountsRequest{AfterAccountId: after, Limit: 200})
	if e != nil {
		return nil, "", ErrUnavailable
	}
	out := make([]*Account, 0, len(r.Accounts))
	for _, a := range r.Accounts {
		out = append(out, &Account{ID: a.AccountId, DisplayName: a.DisplayName, Handle: a.Handle, Country: a.Country})
	}
	return out, r.NextAfterAccountId, nil
}
