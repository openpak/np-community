package xmpp

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"openpak/np-common/coreclient"
	"openpak/np-community/community"
)

// Poller is the core's event poll (the same one np-account follows for bans).
type Poller interface {
	PollEvents(ctx context.Context, sinceVersion uint64, limit int) ([]coreclient.Event, error)
}

const followBatch = 1000

// FollowChat follows the core's event stream for type "message" (a chat message was sent to
// event.AccountID, subject = the chat message id) and delivers each to the recipient's open XMPP
// streams. A recipient with no open stream is left alone: chat is the store, and the account's
// delivery cursor makes the message pending for the next sign-in or stored-fetch.
//
// The position in the stream is kept (np_chat_follow). On the very first start it is set to the
// head of the stream without processing, so messages sent before this existed are not replayed.
func (s *Server) FollowChat(ctx context.Context, core Poller, interval time.Duration) {
	s.mu.Lock()
	if s.Log == nil {
		s.Log = slog.Default()
	}
	s.mu.Unlock()
	svc := s.Service
	if svc.Chat == nil || svc.Inbox == nil {
		return
	}
	version, ok, err := svc.Inbox.Version(ctx)
	for err != nil && ctx.Err() == nil {
		s.Log.Warn("chat follower: read position failed", "error", err)
		sleep(ctx, interval)
		version, ok, err = svc.Inbox.Version(ctx)
	}
	if !ok {
		version = s.fastForward(ctx, core, interval)
	}
	s.Log.Info("chat follower started", "since_version", version)
	for ctx.Err() == nil {
		evs, err := core.PollEvents(ctx, version, followBatch)
		if err != nil {
			s.Log.Warn("chat follower: poll failed", "error", err)
			sleep(ctx, interval)
			continue
		}
		for _, e := range evs {
			if e.Type == "message" {
				if err := s.onMessageEvent(ctx, e); err != nil && (errors.Is(err, community.ErrChatUnavailable) || errors.Is(err, coreclient.ErrUnavailable)) {
					s.Log.Warn("chat follower: delivery failed, will retry", "account", e.AccountID, "message", e.SubjectID, "error", err)
					break
				} else if err != nil {
					s.Log.Warn("chat follower: message skipped", "account", e.AccountID, "message", e.SubjectID, "error", err)
				}
			}
			version = e.Version
			if err := svc.Inbox.SetVersion(ctx, version); err != nil {
				s.Log.Warn("chat follower: save position failed", "error", err)
			}
		}
		if len(evs) < followBatch {
			sleep(ctx, interval)
		}
	}
}

func (s *Server) fastForward(ctx context.Context, core Poller, interval time.Duration) uint64 {
	var version uint64
	for ctx.Err() == nil {
		evs, err := core.PollEvents(ctx, version, followBatch)
		if err != nil {
			s.Log.Warn("chat follower: poll failed", "error", err)
			sleep(ctx, interval)
			continue
		}
		if len(evs) > 0 {
			version = evs[len(evs)-1].Version
		}
		if len(evs) < followBatch {
			break
		}
	}
	s.Service.Inbox.SetVersion(ctx, version)
	return version
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func (s *Server) onMessageEvent(ctx context.Context, e coreclient.Event) error {
	id, err := strconv.ParseInt(e.SubjectID, 10, 64)
	if err != nil || e.AccountID == "" {
		return nil // not ours to understand
	}
	svc := s.Service
	m, err := svc.Chat.Get(ctx, id)
	if errors.Is(err, community.ErrChatNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.To != e.AccountID {
		return nil
	}
	// First message for this account since the follower exists: make it pending.
	if _, ok, err := svc.Inbox.Cursor(ctx, e.AccountID); err != nil {
		return err
	} else if !ok {
		if err := svc.Inbox.Advance(ctx, e.AccountID, id-1); err != nil {
			return err
		}
	}
	streams := s.each(e.AccountID, nil, func(*client) bool { return true })
	if streams == 0 {
		s.Log.Info("NP message pending", "account", e.AccountID, "chat_id", id)
		return nil
	}
	n, err := s.drain(ctx, e.AccountID, nil, id)
	if err != nil {
		return err
	}
	s.Log.Info("NP message in", "account", e.AccountID, "chat_id", id, "streams", streams, "delivered", n)
	return nil
}
