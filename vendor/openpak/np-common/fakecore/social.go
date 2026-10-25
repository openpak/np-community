package fakecore

import (
	"context"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	accountv1 "openpak/np-common/accountpb"
	"sort"
	"time"
)

type fakeSession struct {
	Account, Namespace, Title, Client, OS string
	Expiry                                time.Time
}

func pair(a, b string) [2]string {
	if a > b {
		a, b = b, a
	}
	return [2]string{a, b}
}
func (c *Core) AddFriend(a, b string) { c.mu.Lock(); defer c.mu.Unlock(); c.friends[pair(a, b)] = true }
func (c *Core) SetBlocked(a, b string, blocked bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.blocks[[2]string{a, b}] = blocked
}
func (c *Core) ListAccounts(ctx context.Context, r *accountv1.ListAccountsRequest) (*accountv1.ListAccountsResponse, error) {
	if e := c.enter(ctx, "Identity.ListAccounts"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := []string{}
	for id, a := range c.accounts {
		if id > r.AfterAccountId && a.Status == accountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE {
			keys = append(keys, id)
		}
	}
	sort.Strings(keys)
	limit := int(r.Limit)
	if limit <= 0 {
		limit = 100
	}
	out := &accountv1.ListAccountsResponse{}
	for _, id := range keys[:min(limit, len(keys))] {
		a := c.accounts[id]
		out.Accounts = append(out.Accounts, &accountv1.AccountProjection{AccountId: id, DisplayName: a.DisplayName, Country: a.Country})
	}
	if len(keys) > limit {
		out.NextAfterAccountId = keys[limit-1]
	}
	return out, nil
}
func (c *Core) ListFriends(ctx context.Context, r *accountv1.ListFriendsRequest) (*accountv1.ListFriendsResponse, error) {
	if e := c.enter(ctx, "Social.ListFriends"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := &accountv1.ListFriendsResponse{}
	for p, yes := range c.friends {
		if !yes {
			continue
		}
		id := ""
		if p[0] == r.AccountId {
			id = p[1]
		} else if p[1] == r.AccountId {
			id = p[0]
		}
		if id != "" {
			out.Friends = append(out.Friends, &accountv1.Friend{AccountId: id, DisplayName: c.accounts[id].DisplayName})
		}
	}
	return out, nil
}
func (c *Core) CheckInteraction(ctx context.Context, r *accountv1.CheckInteractionRequest) (*accountv1.CheckInteractionResponse, error) {
	if e := c.enter(ctx, "Social.CheckInteraction"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	a, b := c.accounts[r.ActorId], c.accounts[r.TargetId]
	allowed := a != nil && b != nil && a.Status == accountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE && b.Status == accountv1.AccountStatus_ACCOUNT_STATUS_ACTIVE && c.friends[pair(r.ActorId, r.TargetId)] && !c.blocks[[2]string{r.ActorId, r.TargetId}] && !c.blocks[[2]string{r.TargetId, r.ActorId}]
	return &accountv1.CheckInteractionResponse{Allowed: allowed}, nil
}
func (c *Core) ListIncomingRequests(ctx context.Context, r *accountv1.ListIncomingRequestsRequest) (*accountv1.ListIncomingRequestsResponse, error) {
	if e := c.enter(ctx, "Social.ListIncomingRequests"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := &accountv1.ListIncomingRequestsResponse{}
	for p, yes := range c.pending {
		if yes && p[1] == r.AccountId {
			out.Requesters = append(out.Requesters, &accountv1.Friend{AccountId: p[0], DisplayName: c.accounts[p[0]].DisplayName})
		}
	}
	return out, nil
}
func (c *Core) ListOutgoingRequests(ctx context.Context, r *accountv1.ListOutgoingRequestsRequest) (*accountv1.ListOutgoingRequestsResponse, error) {
	if e := c.enter(ctx, "Social.ListOutgoingRequests"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := &accountv1.ListOutgoingRequestsResponse{}
	for p, yes := range c.pending {
		if yes && p[0] == r.AccountId {
			out.Addressees = append(out.Addressees, &accountv1.Friend{AccountId: p[1], DisplayName: c.accounts[p[1]].DisplayName})
		}
	}
	return out, nil
}
func (c *Core) RequestFriend(ctx context.Context, r *accountv1.RequestFriendRequest) (*accountv1.RequestFriendResponse, error) {
	if e := c.enter(ctx, "Social.RequestFriend"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pending[[2]string{r.RequesterId, r.AddresseeId}] = true
	return &accountv1.RequestFriendResponse{State: accountv1.FriendState_FRIEND_STATE_PENDING}, nil
}
func (c *Core) AcceptFriend(ctx context.Context, r *accountv1.AcceptFriendRequest) (*accountv1.AcceptFriendResponse, error) {
	if e := c.enter(ctx, "Social.AcceptFriend"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	p := [2]string{r.RequesterId, r.AddresseeId}
	if !c.pending[p] {
		return nil, status.Error(codes.FailedPrecondition, "no pending request")
	}
	delete(c.pending, p)
	c.friends[pair(r.RequesterId, r.AddresseeId)] = true
	return &accountv1.AcceptFriendResponse{State: accountv1.FriendState_FRIEND_STATE_ACCEPTED}, nil
}
func (c *Core) RemoveFriend(ctx context.Context, r *accountv1.RemoveFriendRequest) (*accountv1.RemoveFriendResponse, error) {
	if e := c.enter(ctx, "Social.RemoveFriend"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.friends, pair(r.AccountId, r.OtherId))
	delete(c.pending, [2]string{r.AccountId, r.OtherId})
	delete(c.pending, [2]string{r.OtherId, r.AccountId})
	return &accountv1.RemoveFriendResponse{}, nil
}
func (c *Core) RegisterSession(ctx context.Context, r *accountv1.RegisterSessionRequest) (*accountv1.RegisterSessionResponse, error) {
	if e := c.enter(ctx, "Sessions.RegisterSession"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextSession++
	id := fmt.Sprintf("sess-%d", c.nextSession)
	for old, s := range c.sessions {
		if s.Account == r.AccountId && s.Namespace == r.Namespace && s.Title == r.TitleId {
			delete(c.sessions, old)
		}
	}
	c.sessions[id] = fakeSession{r.AccountId, r.Namespace, r.TitleId, r.Client, r.Os, time.Now().Add(time.Minute)}
	return &accountv1.RegisterSessionResponse{SessionId: id, LeaseSeconds: 60}, nil
}
func (c *Core) Heartbeat(ctx context.Context, r *accountv1.HeartbeatRequest) (*accountv1.HeartbeatResponse, error) {
	if e := c.enter(ctx, "Sessions.Heartbeat"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.sessions[r.SessionId]
	if !ok || time.Now().After(s.Expiry) {
		return nil, status.Error(codes.NotFound, "expired")
	}
	s.Expiry = time.Now().Add(time.Minute)
	c.sessions[r.SessionId] = s
	return &accountv1.HeartbeatResponse{LeaseSeconds: 60}, nil
}
func (c *Core) ExpireSession(ctx context.Context, r *accountv1.ExpireSessionRequest) (*accountv1.ExpireSessionResponse, error) {
	if e := c.enter(ctx, "Sessions.ExpireSession"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sessions, r.SessionId)
	return &accountv1.ExpireSessionResponse{}, nil
}
func (c *Core) GetPresence(ctx context.Context, r *accountv1.GetPresenceRequest) (*accountv1.GetPresenceResponse, error) {
	if e := c.enter(ctx, "Sessions.GetPresence"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	want := map[string]bool{}
	for _, id := range r.AccountIds {
		want[id] = true
	}
	out := &accountv1.GetPresenceResponse{}
	for _, s := range c.sessions {
		if want[s.Account] && time.Now().Before(s.Expiry) {
			out.Presence = append(out.Presence, &accountv1.Presence{AccountId: s.Account, Namespace: s.Namespace, TitleId: s.Title, Client: s.Client, Os: s.OS})
		}
	}
	return out, nil
}
func (c *Core) GetRelationship(ctx context.Context, r *accountv1.GetRelationshipRequest) (*accountv1.GetRelationshipResponse, error) {
	if e := c.enter(ctx, "Social.GetRelationship"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	rel := accountv1.Relationship_RELATIONSHIP_NONE
	switch {
	case c.blocks[[2]string{r.AccountId, r.OtherId}]:
		rel = accountv1.Relationship_RELATIONSHIP_BLOCKED_BY_SELF
	case c.blocks[[2]string{r.OtherId, r.AccountId}]:
		rel = accountv1.Relationship_RELATIONSHIP_BLOCKED_BY_OTHER
	case c.friends[pair(r.AccountId, r.OtherId)]:
		rel = accountv1.Relationship_RELATIONSHIP_FRIENDS
	case c.pending[[2]string{r.AccountId, r.OtherId}]:
		rel = accountv1.Relationship_RELATIONSHIP_PENDING_OUTGOING
	case c.pending[[2]string{r.OtherId, r.AccountId}]:
		rel = accountv1.Relationship_RELATIONSHIP_PENDING_INCOMING
	}
	return &accountv1.GetRelationshipResponse{Relationship: rel}, nil
}
