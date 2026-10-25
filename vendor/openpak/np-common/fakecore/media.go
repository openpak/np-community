package fakecore

import (
	"context"
	"crypto/sha256"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	accountv1 "openpak/np-common/accountpb"
)

func (c *Core) SetAvatar(id string, png []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.avatars == nil {
		c.avatars = map[string][]byte{}
	}
	c.avatars[id] = append([]byte(nil), png...)
}
func (c *Core) GetAvatar(ctx context.Context, r *accountv1.GetAvatarRequest) (*accountv1.GetAvatarResponse, error) {
	if e := c.enter(ctx, "Media.GetAvatar"); e != nil {
		return nil, e
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	png, ok := c.avatars[r.AccountId]
	if !ok {
		return nil, status.Error(codes.NotFound, "no avatar")
	}
	hash := sha256.Sum256(png)
	version := fmt.Sprintf("%x", hash[:])
	if r.IfNoneMatch == version {
		return &accountv1.GetAvatarResponse{Version: version, NotModified: true}, nil
	}
	return &accountv1.GetAvatarResponse{Png: append([]byte(nil), png...), Version: version}, nil
}
