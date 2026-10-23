// redis_notifier.go — Redis Pub/Sub backed [Notifier].
//
// Fan-out shape:
//
//	channel = "nf:stream:" + workspacePublicID
//	payload = JSON-encoded [Event]
//
// Publish PUBLISHes; Subscribe opens a PSubscribe on the channel and
// forwards decoded events to the returned Go channel. The drop policy
// from [InProcessNotifier] is preserved: if a subscriber's buffer is
// full, we drop the event and queue [KindResync] instead.
package stream

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

const redisChannelPrefix = "nf:stream:"

// RedisNotifier is a [Notifier] backed by Redis Pub/Sub. One
// instance per api process is expected; PubSub connections are
// created per Subscribe call and closed on ctx cancellation.
type RedisNotifier struct {
	rdb *redis.Client
	log *slog.Logger
}

// NewRedisNotifier constructs a RedisNotifier from an existing client.
// Pass a dedicated *redis.Client if you want to isolate stream traffic
// from the sessionstore traffic (recommended in production).
func NewRedisNotifier(rdb *redis.Client, log *slog.Logger) *RedisNotifier {
	if log == nil {
		log = slog.Default()
	}
	return &RedisNotifier{rdb: rdb, log: log}
}

// Publish serialises evt to JSON and PUBLISHes it on the workspace
// channel. Errors are logged and swallowed so the caller's hot path
// is not coupled to Redis availability.
func (n *RedisNotifier) Publish(ctx context.Context, evt Event) {
	b, err := json.Marshal(evt)
	if err != nil {
		n.log.Warn("stream/redis: marshal failed", "err", err)
		return
	}
	if err := n.rdb.Publish(ctx, redisChannelPrefix+evt.WorkspaceID, b).Err(); err != nil {
		n.log.Warn("stream/redis: publish failed", "err", err)
	}
}

// Subscribe opens a PubSub subscription scoped to ctx and returns a
// buffered receive channel. On ctx cancellation the PubSub is closed
// and the outbound channel is closed so the SSE handler unwinds.
func (n *RedisNotifier) Subscribe(ctx context.Context, workspacePublicID string) <-chan Event {
	out := make(chan Event, 64)
	ps := n.rdb.Subscribe(ctx, redisChannelPrefix+workspacePublicID)
	go func() {
		defer close(out)
		defer func() { _ = ps.Close() }()
		ch := ps.Channel()
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-ch:
				if !ok {
					return
				}
				var evt Event
				if err := json.Unmarshal([]byte(msg.Payload), &evt); err != nil {
					n.log.Warn("stream/redis: unmarshal failed", "err", err)
					continue
				}
				offerOrResync(out, evt, workspacePublicID)
			}
		}
	}()
	return out
}

// offerOrResync queues evt on out. When the inbox is full the oldest
// queued event is dropped to make room for a [KindResync] marker, which
// supersedes any single lost event. The caller is the only sender on out,
// so the slot freed here cannot be taken before the marker is queued.
func offerOrResync(out chan Event, evt Event, workspacePublicID string) {
	select {
	case out <- evt:
		return
	default:
	}
	select {
	case <-out:
	default:
	}
	select {
	case out <- Event{Kind: KindResync, WorkspaceID: workspacePublicID, At: evt.At}:
	default:
	}
}
