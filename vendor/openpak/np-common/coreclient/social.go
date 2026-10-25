package coreclient

import (
	"context"
	accountv1 "openpak/np-common/accountpb"
)

// Social uses the canonical core graph. No relationship is stored in the adapter.
func (c *Client) Friends(ctx context.Context, id string) ([]*accountv1.Friend, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, e := accountv1.NewSocialClient(c.conn).ListFriends(ctx, &accountv1.ListFriendsRequest{AccountId: id})
	if e != nil {
		return nil, ErrUnavailable
	}
	return r.Friends, nil
}
func (c *Client) Incoming(ctx context.Context, id string) ([]*accountv1.Friend, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, e := accountv1.NewSocialClient(c.conn).ListIncomingRequests(ctx, &accountv1.ListIncomingRequestsRequest{AccountId: id})
	if e != nil {
		return nil, ErrUnavailable
	}
	return r.Requesters, nil
}
func (c *Client) Outgoing(ctx context.Context, id string) ([]*accountv1.Friend, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, e := accountv1.NewSocialClient(c.conn).ListOutgoingRequests(ctx, &accountv1.ListOutgoingRequestsRequest{AccountId: id})
	if e != nil {
		return nil, ErrUnavailable
	}
	return r.Addressees, nil
}
func (c *Client) CanInteract(ctx context.Context, actor, target string) (bool, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, e := accountv1.NewSocialClient(c.conn).CheckInteraction(ctx, &accountv1.CheckInteractionRequest{ActorId: actor, TargetId: target, Kind: accountv1.InteractionKind_INTERACTION_KIND_DISCOVER})
	if e != nil {
		return false, ErrUnavailable
	}
	return r.Allowed, nil
}
func (c *Client) RequestFriend(ctx context.Context, actor, target string) error {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	_, e := accountv1.NewSocialClient(c.conn).RequestFriend(ctx, &accountv1.RequestFriendRequest{RequesterId: actor, AddresseeId: target})
	return e
}
func (c *Client) AcceptFriend(ctx context.Context, actor, target string) error {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	_, e := accountv1.NewSocialClient(c.conn).AcceptFriend(ctx, &accountv1.AcceptFriendRequest{AddresseeId: actor, RequesterId: target})
	return e
}
func (c *Client) RemoveFriend(ctx context.Context, actor, target string) error {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	_, e := accountv1.NewSocialClient(c.conn).RemoveFriend(ctx, &accountv1.RemoveFriendRequest{AccountId: actor, OtherId: target})
	return e
}
func (c *Client) Presence(ctx context.Context, ids []string) ([]*accountv1.Presence, error) {
	var out []*accountv1.Presence
	for start := 0; start < len(ids); start += 200 {
		end := min(start+200, len(ids))
		call, cancel := c.ctx(ctx)
		r, e := accountv1.NewSessionsClient(c.conn).GetPresence(call, &accountv1.GetPresenceRequest{AccountIds: ids[start:end]})
		cancel()
		if e != nil {
			return nil, ErrUnavailable
		}
		out = append(out, r.Presence...)
	}
	return out, nil
}
func (c *Client) RegisterPresence(ctx context.Context, account, title, endpoint, client, os string) (string, int32, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, e := accountv1.NewSessionsClient(c.conn).RegisterSession(ctx, &accountv1.RegisterSessionRequest{AccountId: account, Namespace: Namespace, TitleId: title, EndpointRef: endpoint, LeaseSeconds: 60, Client: client, Os: os})
	if e != nil {
		return "", 0, ErrUnavailable
	}
	if r.SessionId == "" || r.LeaseSeconds < 1 {
		return "", 0, ErrUnavailable
	}
	return r.SessionId, r.LeaseSeconds, nil
}
func (c *Client) HeartbeatPresence(ctx context.Context, id string) (int32, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, e := accountv1.NewSessionsClient(c.conn).Heartbeat(ctx, &accountv1.HeartbeatRequest{SessionId: id})
	if e != nil {
		return 0, ErrUnavailable
	}
	if r.LeaseSeconds < 1 {
		return 0, ErrUnavailable
	}
	return r.LeaseSeconds, nil
}
func (c *Client) ExpirePresence(ctx context.Context, id string) error {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	_, e := accountv1.NewSessionsClient(c.conn).ExpireSession(ctx, &accountv1.ExpireSessionRequest{SessionId: id})
	return e
}

func (c *Client) Relationship(ctx context.Context, actor, target string) (accountv1.Relationship, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, e := accountv1.NewSocialClient(c.conn).GetRelationship(ctx, &accountv1.GetRelationshipRequest{AccountId: actor, OtherId: target})
	if e != nil {
		return 0, ErrUnavailable
	}
	return r.Relationship, nil
}
