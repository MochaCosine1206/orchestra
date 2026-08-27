package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// StartPoller runs the Telegram long-poll loop in a goroutine.
func (b *Bridge) StartPoller(ctx context.Context) {
	go b.pollLoop(ctx)
}

func (b *Bridge) pollLoop(ctx context.Context) {
	slog.Info("poller started")

	// Load persisted offset
	b.offset = b.loadOffset()
	if b.offset == 0 {
		// First run: flush old updates
		newOffset, err := b.tg.FlushOffset(ctx)
		if err != nil {
			slog.Warn("failed to flush offset", "err", err)
		} else {
			b.offset = newOffset
			b.persistOffset()
		}
	}

	backoff := time.Second

	for {
		select {
		case <-ctx.Done():
			slog.Info("poller stopped")
			return
		default:
		}

		updates, err := b.tg.GetUpdates(ctx, b.offset, b.cfg.PollTimeoutSecs)
		if err != nil {
			if ctx.Err() != nil {
				return // context cancelled
			}
			slog.Error("getUpdates error", "err", err, "backoff", backoff)
			time.Sleep(backoff)
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}

		backoff = time.Second // reset on success

		for _, u := range updates {
			b.offset = u.UpdateID + 1
			b.persistOffset()
			b.router.Dispatch(ctx, u)
		}
	}
}

func (b *Bridge) loadOffset() int64 {
	data, err := os.ReadFile(b.cfg.OffsetFile)
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(data))
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func (b *Bridge) persistOffset() {
	os.MkdirAll(b.cfg.StateBase, 0755)
	os.WriteFile(b.cfg.OffsetFile, []byte(fmt.Sprintf("%d", b.offset)), 0644)
}
