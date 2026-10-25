// Package coreclient is np-account's client for the account core's internal
// gRPC. np-account owns no identity: every credential check, account fact
// and link lives in the core, and this is the only package that talks to it.
// Core failures other than the documented refusals are reported as
// ErrUnavailable so callers fail closed.
package coreclient

import (
	"context"
	"errors"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	accountv1 "openpak/np-common/accountpb"
)

const (
	// Namespace is the adapter namespace of every call and link. The link
	// subject is the online ID.
	Namespace = "np"
	// Purpose is sent with every credential check.
	Purpose = "console_login"

	callTimeout = 5 * time.Second
)

var (
	ErrInvalidCredentials  = errors.New("invalid credentials")
	ErrAccountDisabled     = errors.New("account banned or deleted")
	ErrInvalidSecondFactor = errors.New("invalid second factor")
	ErrRateLimited         = errors.New("too many attempts")
	ErrNotFound            = errors.New("not found")
	ErrNoLink              = errors.New("no active np link")
	ErrOnlineIDTaken       = errors.New("online ID already taken")
	ErrUnavailable         = errors.New("account core unavailable")
)

// Verified is the result of a correct password.
type Verified struct {
	AccountID         string
	Country           string
	TwoFactorRequired bool
}

// Account is the core's view of an active account.
type Account struct {
	ID          string
	DisplayName string
	Handle      string
	Country     string
	Birthday    string // ISO 8601 date, empty when unset
}

// Link is an active np link: the online ID bound to a core account.
type Link struct {
	LinkID    string
	AccountID string
	OnlineID  string
}

type Client struct {
	conn   *grpc.ClientConn
	key    string
	ident  accountv1.IdentityClient
	links  accountv1.LinksClient
	events accountv1.EventsClient
}

// Dial connects to the core at addr (plaintext; the core is on the internal
// network). Extra options override the defaults, which is how tests supply an
// in-process dialer.
func Dial(addr, internalKey string, opts ...grpc.DialOption) (*Client, error) {
	opts = append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, opts...)
	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, err
	}
	return &Client{
		conn: conn, key: internalKey,
		ident:  accountv1.NewIdentityClient(conn),
		links:  accountv1.NewLinksClient(conn),
		events: accountv1.NewEventsClient(conn),
	}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

func (c *Client) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, callTimeout)
	return metadata.AppendToOutgoingContext(ctx, "authorization", "Bearer "+c.key), cancel
}

// VerifyPassword checks an email and password as namespace np. The caller
// declares second-factor support, so an account with two-factor comes back
// with TwoFactorRequired and VerifySecondFactor is owed before sign-in.
func (c *Client) VerifyPassword(ctx context.Context, email, password string) (*Verified, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	resp, err := c.ident.VerifyPassword(ctx, &accountv1.VerifyPasswordRequest{
		CallerNamespace: Namespace, Purpose: Purpose,
		Email: email, Password: password, SecondFactorSupported: true,
	})
	if err != nil {
		if status.Code(err) == codes.Unauthenticated {
			return nil, ErrInvalidCredentials
		}
		return nil, ErrUnavailable
	}
	if resp.Status != accountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE || resp.AccountId == "" {
		if resp.Status == accountv1.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED {
			return nil, ErrUnavailable
		}
		return nil, ErrAccountDisabled
	}
	return &Verified{AccountID: resp.AccountId, Country: resp.Country, TwoFactorRequired: resp.TwoFactorRequired}, nil
}

// VerifySecondFactor completes a password check for a two-factor account.
// Exactly one of code (TOTP) and recoveryCode is used; code wins.
func (c *Client) VerifySecondFactor(ctx context.Context, accountID, code, recoveryCode string) error {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	req := &accountv1.VerifySecondFactorRequest{AccountId: accountID}
	if code != "" {
		req.Code = code
	} else {
		req.RecoveryCode = recoveryCode
	}
	_, err := c.ident.VerifySecondFactor(ctx, req)
	switch status.Code(err) {
	case codes.OK:
		return nil
	case codes.Unauthenticated, codes.InvalidArgument:
		return ErrInvalidSecondFactor
	case codes.ResourceExhausted:
		return ErrRateLimited
	default:
		return ErrUnavailable
	}
}

// GetAccount returns an active account. A banned, deleted or
// deletion-pending account is ErrAccountDisabled.
func (c *Client) GetAccount(ctx context.Context, accountID string) (*Account, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	resp, err := c.ident.GetAccount(ctx, &accountv1.GetAccountRequest{AccountId: accountID})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, ErrUnavailable
	}
	if resp.Status != accountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE {
		return nil, ErrAccountDisabled
	}
	return &Account{ID: resp.AccountId, DisplayName: resp.DisplayName, Handle: resp.Handle, Country: resp.Country, Birthday: resp.Birthday}, nil
}

// GetLinkByAccount returns the account's active np link, or ErrNoLink.
func (c *Client) GetLinkByAccount(ctx context.Context, accountID string) (*Link, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	resp, err := c.links.GetActiveLinkByAccount(ctx, &accountv1.GetActiveLinkByAccountRequest{
		Namespace: Namespace, AccountId: accountID,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNoLink
		}
		return nil, ErrUnavailable
	}
	if resp.State != accountv1.LinkState_LINK_STATE_ACTIVE {
		return nil, ErrNoLink
	}
	return &Link{LinkID: resp.LinkId, AccountID: resp.AccountId, OnlineID: resp.SubjectId}, nil
}

// GetLinkByOnlineID returns the active np link for an online ID, or ErrNoLink.
func (c *Client) GetLinkByOnlineID(ctx context.Context, onlineID string) (*Link, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	resp, err := c.links.GetLinkBySubject(ctx, &accountv1.GetLinkBySubjectRequest{
		Namespace: Namespace, SubjectId: onlineID,
	})
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return nil, ErrNoLink
		}
		return nil, ErrUnavailable
	}
	if resp.State != accountv1.LinkState_LINK_STATE_ACTIVE {
		return nil, ErrNoLink
	}
	return &Link{LinkID: resp.LinkId, AccountID: resp.AccountId, OnlineID: onlineID}, nil
}

// ClaimOnlineID binds a new online ID to the account: reserve, activate, then
// publish the ID as the link's public code. The core keys public codes on
// lower-cased letters and digits, so that last step is what makes online IDs
// unique ignoring case. If it is refused, the link is revoked again and the
// ID is ErrOnlineIDTaken. The caller has already checked onlineid.Valid.
func (c *Client) ClaimOnlineID(ctx context.Context, accountID, onlineID string) (*Link, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	idem := "np-claim-" + accountID + "-" + strings.ToLower(onlineID)
	res, err := c.links.ReserveLink(ctx, &accountv1.ReserveLinkRequest{
		AccountId: accountID, Namespace: Namespace, SubjectId: onlineID, IdempotencyKey: idem,
	})
	if err != nil {
		if status.Code(err) == codes.AlreadyExists {
			return nil, ErrOnlineIDTaken
		}
		return nil, ErrUnavailable
	}
	if _, err := c.links.ActivateLink(ctx, &accountv1.ActivateLinkRequest{LinkId: res.LinkId, IdempotencyKey: idem}); err != nil {
		return nil, ErrUnavailable
	}
	_, err = c.links.SetLinkPublicCode(ctx, &accountv1.SetLinkPublicCodeRequest{LinkId: res.LinkId, PublicCode: onlineID})
	if err != nil {
		// Best effort; a stuck active link without a code is harmless to retry.
		c.links.UnlinkLink(ctx, &accountv1.UnlinkLinkRequest{LinkId: res.LinkId, IdempotencyKey: idem + "-undo"})
		if status.Code(err) == codes.AlreadyExists {
			return nil, ErrOnlineIDTaken
		}
		return nil, ErrUnavailable
	}
	return &Link{LinkID: res.LinkId, AccountID: accountID, OnlineID: onlineID}, nil
}

// Event is one entry of the core's invalidation stream.
type Event struct {
	Version   uint64
	ID        string
	Type      string
	AccountID string
	Namespace string
	SubjectID string
}

// PollEvents returns up to limit events after sinceVersion, in version order.
func (c *Client) PollEvents(ctx context.Context, sinceVersion uint64, limit int) ([]Event, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	resp, err := c.events.PollEvents(ctx, &accountv1.PollEventsRequest{SinceVersion: sinceVersion, Limit: int32(limit)})
	if err != nil {
		return nil, ErrUnavailable
	}
	out := make([]Event, 0, len(resp.GetEvents()))
	for _, e := range resp.GetEvents() {
		out = append(out, Event{Version: e.GetVersion(), ID: e.GetEventId(), Type: e.GetType(), AccountID: e.GetAccountId(), Namespace: e.GetNamespace(), SubjectID: e.GetSubjectId()})
	}
	return out, nil
}
