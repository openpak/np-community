// Package fakecore is an in-process stand-in for the account core's internal
// gRPC (Identity and Links), for tests. It implements only the calls np-account
// makes, with the core's documented behaviour: bearer-key auth, two-factor
// gate on VerifyPassword, public-code uniqueness per namespace.
package fakecore

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	accountv1 "github.com/openpak/account-api/openpak/account/v1"
)

// Account is a fake core account.
type Account struct {
	ID          string
	Email       string
	Password    string
	DisplayName string
	Country     string
	Status      accountv1.AccountStatus
	// TOTP, when set, makes the account a two-factor account; it is the
	// only code VerifySecondFactor accepts.
	TOTP string
}

type link struct {
	id, account, namespace, subject, code string
	state                                 accountv1.LinkState
}

type Core struct {
	accountv1.UnimplementedIdentityServer
	accountv1.UnimplementedLinksServer
	accountv1.UnimplementedSocialServer
	accountv1.UnimplementedSessionsServer
	accountv1.UnimplementedMediaServer

	key string

	mu          sync.Mutex
	accounts    map[string]*Account
	links       map[string]*link
	friends     map[[2]string]bool
	pending     map[[2]string]bool
	blocks      map[[2]string]bool
	sessions    map[string]fakeSession
	avatars     map[string][]byte
	nextSession int
	nextLink    int
	// Down makes every call fail with Unavailable.
	Down bool
	// Calls records "Service.Method" per call, in order.
	Calls []string
}

func New(key string) *Core {
	return &Core{key: key, accounts: map[string]*Account{}, links: map[string]*link{}, friends: map[[2]string]bool{}, pending: map[[2]string]bool{}, blocks: map[[2]string]bool{}, sessions: map[string]fakeSession{}, avatars: map[string][]byte{}}
}

// AddAccount registers a. An empty Status means active.
func (c *Core) AddAccount(a Account) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if a.Status == accountv1.AccountStatus_ACCOUNT_STATUS_UNSPECIFIED {
		a.Status = accountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE
	}
	c.accounts[a.ID] = &a
}

// SetStatus changes an account's status.
func (c *Core) SetStatus(id string, s accountv1.AccountStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accounts[id].Status = s
}

// Link returns the link for (np, subject), if any.
func (c *Core) Link(subject string) (id, account string, state accountv1.LinkState, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.links {
		if l.subject == subject {
			return l.id, l.account, l.state, true
		}
	}
	return "", "", 0, false
}

// Called reports how many recorded calls equal name.
func (c *Core) Called(name string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, x := range c.Calls {
		if x == name {
			n++
		}
	}
	return n
}

func (c *Core) enter(ctx context.Context, name string) error {
	c.mu.Lock()
	c.Calls = append(c.Calls, name)
	down := c.Down
	c.mu.Unlock()
	if down {
		return status.Error(codes.Unavailable, "core down")
	}
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get("authorization"); len(v) != 1 || v[0] != "Bearer "+c.key {
		return status.Error(codes.Unauthenticated, "bad internal key")
	}
	return nil
}

// Serve starts the core on an in-process listener and returns the dial
// options a client needs to reach it. The server stops when t ends.
func (c *Core) Serve(t testing.TB) []grpc.DialOption {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	accountv1.RegisterIdentityServer(srv, c)
	accountv1.RegisterLinksServer(srv, c)
	accountv1.RegisterSocialServer(srv, c)
	accountv1.RegisterSessionsServer(srv, c)
	accountv1.RegisterMediaServer(srv, c)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	return []grpc.DialOption{grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() })}
}

func (c *Core) VerifyPassword(ctx context.Context, req *accountv1.VerifyPasswordRequest) (*accountv1.VerifyPasswordResponse, error) {
	if err := c.enter(ctx, "Identity.VerifyPassword"); err != nil {
		return nil, err
	}
	if req.CallerNamespace == "" || req.Purpose == "" {
		return nil, status.Error(codes.InvalidArgument, "caller_namespace and purpose are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, a := range c.accounts {
		if a.Email != req.Email || a.Password != req.Password {
			continue
		}
		if a.Status != accountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE {
			return &accountv1.VerifyPasswordResponse{Status: a.Status}, nil
		}
		if a.TOTP != "" && !req.SecondFactorSupported {
			return nil, status.Error(codes.FailedPrecondition, "two-factor authentication required")
		}
		return &accountv1.VerifyPasswordResponse{
			AccountId: a.ID, Status: a.Status, Country: a.Country, TwoFactorRequired: a.TOTP != "",
		}, nil
	}
	return nil, status.Error(codes.Unauthenticated, "invalid credentials")
}

func (c *Core) VerifySecondFactor(ctx context.Context, req *accountv1.VerifySecondFactorRequest) (*accountv1.VerifySecondFactorResponse, error) {
	if err := c.enter(ctx, "Identity.VerifySecondFactor"); err != nil {
		return nil, err
	}
	if req.AccountId == "" || (req.Code == "" && req.RecoveryCode == "") {
		return nil, status.Error(codes.InvalidArgument, "account_id and a code or recovery_code are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.accounts[req.AccountId]
	if a == nil || a.TOTP == "" {
		return nil, status.Error(codes.FailedPrecondition, "no second factor")
	}
	if req.Code != a.TOTP {
		return nil, status.Error(codes.Unauthenticated, "invalid code")
	}
	return &accountv1.VerifySecondFactorResponse{}, nil
}

func (c *Core) GetAccount(ctx context.Context, req *accountv1.GetAccountRequest) (*accountv1.GetAccountResponse, error) {
	if err := c.enter(ctx, "Identity.GetAccount"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.accounts[req.AccountId]
	if a == nil {
		return nil, status.Error(codes.NotFound, "account not found")
	}
	return &accountv1.GetAccountResponse{
		AccountId: a.ID, Status: a.Status, DisplayName: a.DisplayName, Country: a.Country, TwoFactorEnabled: a.TOTP != "",
	}, nil
}

func (c *Core) ReserveLink(ctx context.Context, req *accountv1.ReserveLinkRequest) (*accountv1.ReserveLinkResponse, error) {
	if err := c.enter(ctx, "Links.ReserveLink"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.accounts[req.AccountId] == nil {
		return nil, status.Error(codes.NotFound, "account unknown")
	}
	for _, l := range c.links {
		if l.namespace == req.Namespace && l.subject == req.SubjectId && l.state != accountv1.LinkState_LINK_STATE_REVOKED {
			if l.account == req.AccountId {
				return &accountv1.ReserveLinkResponse{LinkId: l.id, State: l.state}, nil
			}
			return nil, status.Error(codes.AlreadyExists, "subject linked to another account")
		}
	}
	c.nextLink++
	l := &link{id: "link-" + strconv.Itoa(c.nextLink), account: req.AccountId, namespace: req.Namespace,
		subject: req.SubjectId, state: accountv1.LinkState_LINK_STATE_PENDING}
	c.links[l.id] = l
	return &accountv1.ReserveLinkResponse{LinkId: l.id, State: l.state}, nil
}

func (c *Core) ActivateLink(ctx context.Context, req *accountv1.ActivateLinkRequest) (*accountv1.ActivateLinkResponse, error) {
	if err := c.enter(ctx, "Links.ActivateLink"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.links[req.LinkId]
	if l == nil {
		return nil, status.Error(codes.NotFound, "link not found")
	}
	if l.state == accountv1.LinkState_LINK_STATE_PENDING {
		l.state = accountv1.LinkState_LINK_STATE_ACTIVE
	}
	return &accountv1.ActivateLinkResponse{State: l.state}, nil
}

func (c *Core) UnlinkLink(ctx context.Context, req *accountv1.UnlinkLinkRequest) (*accountv1.UnlinkLinkResponse, error) {
	if err := c.enter(ctx, "Links.UnlinkLink"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.links[req.LinkId]
	if l == nil {
		return nil, status.Error(codes.NotFound, "link not found")
	}
	l.state, l.code = accountv1.LinkState_LINK_STATE_REVOKED, ""
	return &accountv1.UnlinkLinkResponse{State: l.state}, nil
}

func (c *Core) GetLinkBySubject(ctx context.Context, req *accountv1.GetLinkBySubjectRequest) (*accountv1.GetLinkBySubjectResponse, error) {
	if err := c.enter(ctx, "Links.GetLinkBySubject"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.links {
		if l.namespace == req.Namespace && l.subject == req.SubjectId && l.state != accountv1.LinkState_LINK_STATE_REVOKED {
			return &accountv1.GetLinkBySubjectResponse{LinkId: l.id, AccountId: l.account, State: l.state, PublicCode: l.code}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "link not found")
}

func (c *Core) GetActiveLinkByAccount(ctx context.Context, req *accountv1.GetActiveLinkByAccountRequest) (*accountv1.GetLinkBySubjectResponse, error) {
	if err := c.enter(ctx, "Links.GetActiveLinkByAccount"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, l := range c.links {
		if l.namespace == req.Namespace && l.account == req.AccountId && l.state == accountv1.LinkState_LINK_STATE_ACTIVE {
			return &accountv1.GetLinkBySubjectResponse{LinkId: l.id, AccountId: l.account, State: l.state, SubjectId: l.subject, PublicCode: l.code}, nil
		}
	}
	return nil, status.Error(codes.NotFound, "no active link")
}

// reduce mirrors the core's lookup key: lower-case letters and digits only.
func reduce(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (c *Core) SetLinkPublicCode(ctx context.Context, req *accountv1.SetLinkPublicCodeRequest) (*accountv1.SetLinkPublicCodeResponse, error) {
	if err := c.enter(ctx, "Links.SetLinkPublicCode"); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.links[req.LinkId]
	if l == nil {
		return nil, status.Error(codes.NotFound, "link not found")
	}
	if l.state != accountv1.LinkState_LINK_STATE_ACTIVE {
		return nil, status.Error(codes.FailedPrecondition, "link not active")
	}
	for _, o := range c.links {
		if o != l && o.namespace == l.namespace && o.state == accountv1.LinkState_LINK_STATE_ACTIVE &&
			o.code != "" && reduce(o.code) == reduce(req.PublicCode) {
			return nil, status.Error(codes.AlreadyExists, "code taken")
		}
	}
	l.code = req.PublicCode
	return &accountv1.SetLinkPublicCodeResponse{}, nil
}
