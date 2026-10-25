package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"openpak/np-common/coreclient"
	"openpak/np-common/onlineid"
)

// EnsureNPLink provisions a real core link without requiring a console login.
// Existing IDs are never renamed. A collision uses an account-derived suffix.
type ProvisionCore interface {
	GetLinkByAccount(context.Context, string) (*coreclient.Link, error)
	ClaimOnlineID(context.Context, string, string) (*coreclient.Link, error)
}

func EnsureNPLink(ctx context.Context, core ProvisionCore, acct *coreclient.Account) (*coreclient.Link, error) {
	link, e := core.GetLinkByAccount(ctx, acct.ID)
	if e == nil {
		return link, nil
	}
	if !errors.Is(e, coreclient.ErrNoLink) {
		return nil, e
	}
	preferred, ok := onlineid.FromDisplayName(acct.DisplayName)
	h := sha256.Sum256([]byte("openpak-np-online-id-v1\x00" + acct.ID))
	suffix := hex.EncodeToString(h[:])
	candidates := []string{}
	if ok {
		candidates = append(candidates, preferred)
	} else {
		preferred = "Player"
	}
	base := preferred
	if len(base) > 7 {
		base = base[:7]
	}
	candidates = append(candidates, base+"_"+suffix[:8], "OP"+suffix[:14])
	if acct.Handle != "" {
		candidates = []string{acct.Handle}
	}
	for _, candidate := range candidates {
		link, e = core.ClaimOnlineID(ctx, acct.ID, candidate)
		if e == nil {
			return link, nil
		}
		// Another reconciler/sign-in may have created the link while we raced.
		existing, lookup := core.GetLinkByAccount(ctx, acct.ID)
		if lookup == nil {
			return existing, nil
		}
		if !errors.Is(lookup, coreclient.ErrNoLink) {
			return nil, lookup
		}
		if !errors.Is(e, coreclient.ErrOnlineIDTaken) {
			return nil, e
		}
	}
	return nil, coreclient.ErrOnlineIDTaken
}
