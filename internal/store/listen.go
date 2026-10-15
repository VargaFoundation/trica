package store

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// listenRetry is the first wait before reconnecting a lost listener; it
// doubles up to listenRetryMax. Tests shorten it.
var (
	listenRetry    = time.Second
	listenRetryMax = 30 * time.Second
)

// Listen calls onEvent for every notification on channel until ctx ends.
//
// It holds its own connection, outside the pool: a LISTEN lives as long as the
// session, and a pooled connection would carry it to whoever borrows it next.
// onEvent is also called each time the listener (re)starts listening, because
// notifications sent while it was not are lost: a caller that only invalidates
// a cache errs on the safe side. onEvent must not block.
func (s *Store) Listen(ctx context.Context, channel string, onEvent func(), log *slog.Logger) error {
	wait := listenRetry
	for {
		err := s.listenOnce(ctx, channel, onEvent, func() { wait = listenRetry })
		if ctx.Err() != nil {
			return nil
		}
		log.Warn("store: listener lost, reconnecting", "channel", channel, "err", err, "retry_in", wait)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(wait):
		}
		wait = min(2*wait, listenRetryMax)
	}
}

// listenOnce listens on one connection until it fails; it calls listening
// once the LISTEN is in place.
func (s *Store) listenOnce(ctx context.Context, channel string, onEvent, listening func()) error {
	conn, err := pgx.ConnectConfig(ctx, s.Pool.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := conn.Exec(ctx, "LISTEN "+pgx.Identifier{channel}.Sanitize()); err != nil {
		return err
	}
	listening()
	onEvent()
	for {
		if _, err := conn.WaitForNotification(ctx); err != nil {
			return err
		}
		onEvent()
	}
}
