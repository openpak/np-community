package community

import (
	"context"
	"errors"
	"sort"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"openpak/np-community/internal/chatpb"
)

// Friend messages ride the core-neutral chat service (gRPC openpak.chat.v1.Chat).
// A PS3 message to a friend on any OpenPak network is one chat row; a message
// from any network to a PS3 user arrives as a core "message" event and is
// delivered by the XMPP server. Chat is the store: nothing is queued here.
const (
	// StoredTTL is how long a replayed message carries in its stored marker, and how far back a
	// sign-in replay looks. The firmware's own limit is unknown; this is OpenPak's choice.
	StoredTTL = 30 * 24 * time.Hour
	// MaxReplay bounds one replay; older pending messages are skipped (and marked delivered).
	MaxReplay = 100
)

var (
	ErrNotFriends      = errors.New("chat: only friends can talk")
	ErrChatRejected    = errors.New("chat: message rejected")
	ErrChatUnavailable = errors.New("chat: service unavailable")
	ErrChatNotFound    = errors.New("chat: no such message")
)

// ChatMessage is one chat row.
type ChatMessage struct {
	ID       int64
	From, To string // core account ids
	Kind     string // text | painting | screenshot | app_data
	Body     string
	SentAt   time.Time
}

// Chat is the part of the chat service np-community uses.
type Chat interface {
	Send(ctx context.Context, from, to, kind, body, dedupKey string) (ChatMessage, bool, error)
	Get(ctx context.Context, id int64) (ChatMessage, error)
	Conversations(ctx context.Context, account string) ([]ChatMessage, error) // newest per peer
	List(ctx context.Context, account, peer string, after int64, limit int32) ([]ChatMessage, error)
}

// ChatClient is the gRPC Chat.
type ChatClient struct {
	cl  chatpb.ChatClient
	key string
	cc  *grpc.ClientConn
}

// DialChat connects to chat at addr (plaintext, internal network). Extra options override the defaults.
func DialChat(addr, key string, opts ...grpc.DialOption) (*ChatClient, error) {
	opts = append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, opts...)
	cc, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, err
	}
	return &ChatClient{cl: chatpb.NewChatClient(cc), key: key, cc: cc}, nil
}

func (c *ChatClient) Close() error { return c.cc.Close() }

func (c *ChatClient) ctx(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	return metadata.AppendToOutgoingContext(ctx, "x-internal-key", c.key), cancel
}

func chatErr(err error) error {
	switch status.Code(err) {
	case codes.PermissionDenied:
		return ErrNotFriends
	case codes.InvalidArgument:
		return ErrChatRejected
	case codes.NotFound:
		return ErrChatNotFound
	}
	return ErrChatUnavailable
}

func fromPB(m *chatpb.Message) ChatMessage {
	return ChatMessage{ID: m.GetId(), From: m.GetFromAccount(), To: m.GetToAccount(), Kind: m.GetKind(), Body: m.GetBody(), SentAt: time.Unix(m.GetSentAtUnix(), 0).UTC()}
}

func (c *ChatClient) Send(ctx context.Context, from, to, kind, body, dedupKey string) (ChatMessage, bool, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, err := c.cl.SendMessage(ctx, &chatpb.SendMessageRequest{FromAccount: from, ToAccount: to, Kind: kind, Body: body, DedupKey: dedupKey})
	if err != nil {
		return ChatMessage{}, false, chatErr(err)
	}
	return fromPB(r.GetMessage()), r.GetDuplicate(), nil
}

func (c *ChatClient) Get(ctx context.Context, id int64) (ChatMessage, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, err := c.cl.GetMessage(ctx, &chatpb.GetMessageRequest{Id: id})
	if err != nil {
		return ChatMessage{}, chatErr(err)
	}
	return fromPB(r.GetMessage()), nil
}

func (c *ChatClient) Conversations(ctx context.Context, account string) ([]ChatMessage, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, err := c.cl.ListConversations(ctx, &chatpb.ListConversationsRequest{Account: account})
	if err != nil {
		return nil, chatErr(err)
	}
	out := make([]ChatMessage, 0, len(r.GetConversations()))
	for _, cv := range r.GetConversations() {
		out = append(out, fromPB(cv.GetNewest()))
	}
	return out, nil
}

func (c *ChatClient) List(ctx context.Context, account, peer string, after int64, limit int32) ([]ChatMessage, error) {
	ctx, cancel := c.ctx(ctx)
	defer cancel()
	r, err := c.cl.ListMessages(ctx, &chatpb.ListMessagesRequest{Account: account, Peer: peer, AfterId: after, Limit: limit})
	if err != nil {
		return nil, chatErr(err)
	}
	out := make([]ChatMessage, 0, len(r.GetMessages()))
	for _, m := range r.GetMessages() {
		out = append(out, fromPB(m))
	}
	return out, nil
}

// PendingMessages returns the messages sent to account that are newer than its delivery cursor, oldest
// first, and the highest message id seen (own sent messages included) so the caller can advance past them.
// An account with no cursor has had no message event since the follower started: nothing is pending, and
// chat history from before is not replayed to a console.
func (s *Service) PendingMessages(ctx context.Context, account string) (msgs []ChatMessage, maxSeen int64, err error) {
	if s.Chat == nil || s.Inbox == nil {
		return nil, 0, ErrChatUnavailable
	}
	cur, ok, err := s.Inbox.Cursor(ctx, account)
	if err != nil || !ok {
		return nil, 0, err
	}
	convs, err := s.Chat.Conversations(ctx, account)
	if err != nil {
		return nil, 0, err
	}
	for _, cv := range convs {
		if cv.ID <= cur {
			continue
		}
		peer := cv.From
		if peer == account {
			peer = cv.To
		}
		list, err := s.Chat.List(ctx, account, peer, cur, 200)
		if err != nil {
			return nil, 0, err
		}
		for _, m := range list {
			if m.ID <= cur {
				continue
			}
			maxSeen = max(maxSeen, m.ID)
			if m.To == account {
				msgs = append(msgs, m)
			}
		}
	}
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].ID < msgs[j].ID })
	return msgs, maxSeen, nil
}

// AccountJID is the NP JID an account is known by: its online id (provisioned if needed) and country.
func (s *Service) AccountJID(ctx context.Context, account string) (string, error) {
	a, err := s.Core.GetAccount(ctx, account)
	if err != nil {
		return "", err
	}
	link, err := s.EnsureLink(ctx, a)
	if err != nil {
		return "", err
	}
	return JID(link.OnlineID, a.Country), nil
}
