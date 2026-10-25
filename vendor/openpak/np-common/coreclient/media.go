package coreclient

import (
	"context"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	accountv1 "github.com/openpak/account-api/openpak/account/v1"
)

type Avatar struct {
	PNG         []byte
	Version     string
	NotModified bool
}

func (c *Client) Avatar(ctx context.Context, id, etag string) (*Avatar, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, e := accountv1.NewMediaClient(c.conn).GetAvatar(ctx, &accountv1.GetAvatarRequest{AccountId: id, Size: 256, IfNoneMatch: etag})
	if e != nil {
		if status.Code(e) == codes.NotFound {
			return nil, ErrNotFound
		}
		return nil, ErrUnavailable
	}
	return &Avatar{PNG: r.Png, Version: r.Version, NotModified: r.NotModified}, nil
}

// PutAvatar publishes canonical website derivatives during an operator backfill.
func (c *Client) PutAvatar(ctx context.Context, req *accountv1.PutAvatarRequest) error {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	_, err := accountv1.NewMediaClient(c.conn).PutAvatar(ctx, req)
	return err
}
