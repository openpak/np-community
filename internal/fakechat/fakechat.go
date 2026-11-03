// Package fakechat is an in-process openpak.chat.v1.Chat gRPC server for tests: friends are declared,
// messages are kept in memory, and the real x-internal-key check and error codes are mirrored.
package fakechat

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"openpak/np-community/community"
	"openpak/np-community/internal/chatpb"
)

type Chat struct {
	chatpb.UnimplementedChatServer
	Key string
	mu  sync.Mutex
	// Down makes every call fail Unavailable.
	Down    bool
	friends map[[2]string]bool
	Msgs    []*chatpb.Message
	dedup   map[string]int64
	Sends   int // SendMessage calls that stored (non-duplicate)
	// Announce, if set, is called for every stored message like the real chat's core announcement.
	Announce func(id int64, to string)
}

func New(key string) *Chat {
	return &Chat{Key: key, friends: map[[2]string]bool{}, dedup: map[string]int64{}}
}

func (c *Chat) AddFriend(a, b string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.friends[[2]string{a, b}], c.friends[[2]string{b, a}] = true, true
}

// Add stores a message as if sent through another surface (website, Wii U) and returns its id.
// It is announced like any send; use AddQuiet for history the follower never heard about.
func (c *Chat) Add(from, to, kind, body string, at time.Time) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.add(from, to, kind, body, at)
}

// AddQuiet stores a message without announcing it.
func (c *Chat) AddQuiet(from, to, kind, body string, at time.Time) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	a := c.Announce
	c.Announce = nil
	defer func() { c.Announce = a }()
	return c.add(from, to, kind, body, at)
}

func (c *Chat) add(from, to, kind, body string, at time.Time) int64 {
	id := int64(len(c.Msgs) + 1)
	c.Msgs = append(c.Msgs, &chatpb.Message{Id: id, FromAccount: from, ToAccount: to, Kind: kind, Body: body, SentAtUnix: at.Unix()})
	if c.Announce != nil {
		c.Announce(id, to)
	}
	return id
}

func (c *Chat) auth(ctx context.Context) error {
	md, _ := metadata.FromIncomingContext(ctx)
	if v := md.Get("x-internal-key"); len(v) == 0 || v[0] != c.Key {
		return status.Error(codes.Unauthenticated, "missing internal credentials")
	}
	if c.Down {
		return status.Error(codes.Unavailable, "down")
	}
	return nil
}

func (c *Chat) SendMessage(ctx context.Context, r *chatpb.SendMessageRequest) (*chatpb.SendMessageResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.auth(ctx); err != nil {
		return nil, err
	}
	if !c.friends[[2]string{r.FromAccount, r.ToAccount}] {
		return nil, status.Error(codes.PermissionDenied, "only friends can talk")
	}
	if r.Body == "" {
		return nil, status.Error(codes.InvalidArgument, "empty message")
	}
	if len(r.Body) > 64<<10 {
		return nil, status.Error(codes.InvalidArgument, "message too large")
	}
	k := r.FromAccount + "\x00" + r.DedupKey
	if r.DedupKey != "" {
		if id, ok := c.dedup[k]; ok {
			return &chatpb.SendMessageResponse{Message: c.Msgs[id-1], Duplicate: true}, nil
		}
	}
	kind := r.Kind
	if kind == "" {
		kind = "text"
	}
	id := c.add(r.FromAccount, r.ToAccount, kind, r.Body, time.Now())
	if r.DedupKey != "" {
		c.dedup[k] = id
	}
	c.Sends++
	return &chatpb.SendMessageResponse{Message: c.Msgs[id-1]}, nil
}

func (c *Chat) GetMessage(ctx context.Context, r *chatpb.GetMessageRequest) (*chatpb.GetMessageResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.auth(ctx); err != nil {
		return nil, err
	}
	if r.Id < 1 || int(r.Id) > len(c.Msgs) {
		return nil, status.Error(codes.NotFound, "no such message")
	}
	return &chatpb.GetMessageResponse{Message: c.Msgs[r.Id-1]}, nil
}

func (c *Chat) ListMessages(ctx context.Context, r *chatpb.ListMessagesRequest) (*chatpb.ListMessagesResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.auth(ctx); err != nil {
		return nil, err
	}
	out := &chatpb.ListMessagesResponse{}
	for _, m := range c.Msgs {
		if m.Id > r.AfterId && ((m.FromAccount == r.Account && m.ToAccount == r.Peer) || (m.FromAccount == r.Peer && m.ToAccount == r.Account)) {
			out.Messages = append(out.Messages, m)
		}
	}
	return out, nil
}

func (c *Chat) ListConversations(ctx context.Context, r *chatpb.ListConversationsRequest) (*chatpb.ListConversationsResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.auth(ctx); err != nil {
		return nil, err
	}
	newest := map[string]*chatpb.Message{}
	for _, m := range c.Msgs {
		peer := m.FromAccount
		if peer == r.Account {
			peer = m.ToAccount
		} else if m.ToAccount != r.Account {
			continue
		}
		newest[peer] = m
	}
	out := &chatpb.ListConversationsResponse{}
	for p, m := range newest {
		out.Conversations = append(out.Conversations, &chatpb.Conversation{Peer: p, Newest: m})
	}
	return out, nil
}

// Dial starts the fake on an in-memory listener and returns a client for it.
func (c *Chat) Dial(t *testing.T) *community.ChatClient {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	chatpb.RegisterChatServer(srv, c)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	cl, err := community.DialChat("passthrough:///chat", c.Key, grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	return cl
}

// Lock and Unlock let a test read Msgs and Sends while the server may be running.
func (c *Chat) Lock()   { c.mu.Lock() }
func (c *Chat) Unlock() { c.mu.Unlock() }

// SetDown makes every call fail Unavailable (or recover).
func (c *Chat) SetDown(down bool) { c.mu.Lock(); c.Down = down; c.mu.Unlock() }

// Unfriend ends chat's friendship (the core's answer) between a and b.
func (c *Chat) Unfriend(a, b string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.friends, [2]string{a, b})
	delete(c.friends, [2]string{b, a})
}
