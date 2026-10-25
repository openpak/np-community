// Package community implements the captured NP login/profile surface and projects
// the canonical OpenPak graph into PS3 identities. See docs/provenance.md.
package community

import (
	"context"
	"errors"
	"fmt"
	"strings"

	accountv1 "github.com/openpak/account-api/openpak/account/v1"
	"openpak/np-common/coreclient"
	"openpak/np-common/identity"
	"openpak/np-common/onlineid"
	"openpak/np-common/ticket"
)

type Core interface {
	GetAccount(context.Context, string) (*coreclient.Account, error)
	GetLinkByAccount(context.Context, string) (*coreclient.Link, error)
	GetLinkByOnlineID(context.Context, string) (*coreclient.Link, error)
	ClaimOnlineID(context.Context, string, string) (*coreclient.Link, error)
	Friends(context.Context, string) ([]*accountv1.Friend, error)
	Incoming(context.Context, string) ([]*accountv1.Friend, error)
	Outgoing(context.Context, string) ([]*accountv1.Friend, error)
	CanInteract(context.Context, string, string) (bool, error)
	Relationship(context.Context, string, string) (accountv1.Relationship, error)
	RequestFriend(context.Context, string, string) error
	AcceptFriend(context.Context, string, string) error
	RemoveFriend(context.Context, string, string) error
	Presence(context.Context, []string) ([]*accountv1.Presence, error)
	RegisterPresence(context.Context, string, string, string, string, string) (string, int32, error)
	HeartbeatPresence(context.Context, string) (int32, error)
	ExpirePresence(context.Context, string) error
}

type Service struct {
	Core          Core
	Tickets       *ticket.Signer
	ResourceKey   []byte // owner-local firmware resource compatibility key
	Client, OS    string
	AvatarBaseURL string
}

func (s *Service) Verify(ctx context.Context, raw []byte) (ticket.Identity, error) {
	return ticket.VerifyAccount(ctx, s.Core, s.Tickets, raw)
}

func Country(country string) string {
	country = strings.ToLower(country)
	if len(country) != 2 || country[0] < 'a' || country[0] > 'z' || country[1] < 'a' || country[1] > 'z' {
		return "us"
	}
	return country
}
func JID(online, country string) string {
	return online + "@" + Country(country) + ".np.community.playstation.net"
}

// Friend projects a canonical OpenPak friendship with a real, provisioned NP link.
type Friend struct {
	AccountID, OnlineID, DisplayName, Country, Subscription string
	Pending                                                 bool
	LastSeenUnix                                            int64
}

func (f Friend) JID() string { return JID(f.OnlineID, f.Country) }
func (s *Service) project(ctx context.Context, f *accountv1.Friend) (Friend, error) {
	a, e := s.Core.GetAccount(ctx, f.AccountId)
	if e != nil {
		return Friend{}, e
	}
	name := a.DisplayName
	if name == "" {
		name = f.DisplayName
	}
	link, e := s.EnsureLink(ctx, a)
	if e != nil {
		return Friend{}, e
	}
	id := link.OnlineID
	if !onlineid.Valid(id) {
		return Friend{}, ticket.ErrInvalid
	}
	if name == "" {
		name = id
	}
	return Friend{AccountID: f.AccountId, OnlineID: id, DisplayName: name, Country: Country(a.Country), Subscription: "both", LastSeenUnix: f.LastSeenUnix}, nil
}
func (s *Service) Roster(ctx context.Context, account string) ([]Friend, error) {
	list, e := s.Core.Friends(ctx, account)
	if e != nil {
		return nil, e
	}
	out := make([]Friend, 0, len(list))
	for _, f := range list {
		allowed, e := s.Core.CanInteract(ctx, account, f.AccountId)
		if e != nil {
			return nil, e
		}
		if !allowed {
			continue
		}
		p, e := s.project(ctx, f)
		if errors.Is(e, coreclient.ErrAccountDisabled) || errors.Is(e, coreclient.ErrNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}
func (s *Service) Pending(ctx context.Context, account string, incoming bool) ([]Friend, error) {
	var list []*accountv1.Friend
	var e error
	if incoming {
		list, e = s.Core.Incoming(ctx, account)
	} else {
		list, e = s.Core.Outgoing(ctx, account)
	}
	if e != nil {
		return nil, e
	}
	out := make([]Friend, 0, len(list))
	for _, f := range list {
		rel, e := s.Core.Relationship(ctx, account, f.AccountId)
		if e != nil {
			return nil, e
		}
		expected := accountv1.Relationship_RELATIONSHIP_PENDING_OUTGOING
		if incoming {
			expected = accountv1.Relationship_RELATIONSHIP_PENDING_INCOMING
		}
		if rel != expected {
			continue
		}
		p, e := s.project(ctx, f)
		if errors.Is(e, coreclient.ErrAccountDisabled) || errors.Is(e, coreclient.ErrNotFound) {
			continue
		}
		if e != nil {
			return nil, e
		}
		p.Subscription = "none"
		p.Pending = true
		out = append(out, p)
	}
	return out, nil
}

// ResolveTarget permits existing graph aliases only for this viewer. A real
// online ID is resolved through the core; a client cannot nominate an account ID.
func (s *Service) ResolveTarget(ctx context.Context, actor, jid string) (string, error) {
	name, domain, ok := strings.Cut(strings.SplitN(jid, "/", 2)[0], "@")
	if !ok || !onlineid.Valid(name) || !strings.HasSuffix(domain, ".playstation.net") {
		return "", ticket.ErrInvalid
	}
	for _, incoming := range []int{0, 1, 2} {
		var fs []Friend
		var e error
		if incoming == 0 {
			fs, e = s.Roster(ctx, actor)
		} else {
			fs, e = s.Pending(ctx, actor, incoming == 1)
		}
		if e != nil {
			return "", e
		}
		for _, f := range fs {
			if strings.EqualFold(f.JID(), jid) {
				return f.AccountID, nil
			}
		}
	}
	link, e := s.Core.GetLinkByOnlineID(ctx, name)
	if e != nil {
		return "", e
	}
	a, e := s.Core.GetAccount(ctx, link.AccountID)
	if e != nil {
		return "", e
	}
	if !strings.EqualFold(JID(link.OnlineID, a.Country), jid) {
		return "", fmt.Errorf("invalid target domain")
	}
	return a.ID, nil
}

// EnsureLink adapts the shared provisioning policy to the community core surface.
func (s *Service) EnsureLink(ctx context.Context, a *coreclient.Account) (*coreclient.Link, error) {
	return identity.EnsureNPLink(ctx, s.Core, a)
}

func (s *Service) Recheck(ctx context.Context, id ticket.Identity) error {
	if _, e := s.Core.GetAccount(ctx, id.AccountID); e != nil {
		return e
	}
	link, e := s.Core.GetLinkByAccount(ctx, id.AccountID)
	if e != nil {
		return e
	}
	if link.LinkID != id.LinkID || link.OnlineID != id.OnlineID {
		return ticket.ErrInvalid
	}
	return nil
}
