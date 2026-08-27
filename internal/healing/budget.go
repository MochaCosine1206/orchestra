package healing

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

const (
	// MaxFixesPerSession is the maximum number of healing fixes allowed per session.
	MaxFixesPerSession = 10
	rollbackTimeout    = 2 * time.Minute
)

// Budget tracks fix attempts per session and handles rollback timers.
type Budget struct {
	sessionID string
	db        *db.DB
	count     int
	mu        sync.Mutex
	timers    map[string]*rollbackTimer
}

type rollbackTimer struct {
	timer    *time.Timer
	cancel   context.CancelFunc
	files    []string
	worktree string
}

// NewBudget creates a Budget for the given session.
func NewBudget(sessionID string, database *db.DB) *Budget {
	return &Budget{
		sessionID: sessionID,
		db:        database,
		timers:    make(map[string]*rollbackTimer),
	}
}

// CanFix returns true if the session budget is not exhausted.
func (b *Budget) CanFix() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.count < MaxFixesPerSession
}

// RecordFix logs a fix attempt to the DB and increments the budget counter.
// It starts a rollback timer that will revert modifiedFiles via git checkout
// if ConfirmFix is not called within 2 minutes.
func (b *Budget) RecordFix(ctx context.Context, taskID string, errorType ErrorType, fixApplied string, modifiedFiles []string, worktreePath string) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	id := fmt.Sprintf("hl-%s-%d", b.sessionID, b.count+1)
	h := db.HealingLog{
		ID:         id,
		SessionID:  b.sessionID,
		TaskID:     sql.NullString{String: taskID, Valid: taskID != ""},
		ErrorType:  sql.NullString{String: string(errorType), Valid: true},
		FixApplied: sql.NullString{String: fixApplied, Valid: fixApplied != ""},
		Success:    0,
		RolledBack: 0,
	}
	if err := b.db.InsertHealingLog(ctx, h); err != nil {
		return err
	}

	b.count++

	// Start rollback timer
	timerCtx, cancel := context.WithCancel(context.Background())
	rt := &rollbackTimer{
		cancel:   cancel,
		files:    modifiedFiles,
		worktree: worktreePath,
	}
	rt.timer = time.AfterFunc(rollbackTimeout, func() {
		select {
		case <-timerCtx.Done():
			return
		default:
			rollbackFiles(worktreePath, modifiedFiles)
		}
	})
	b.timers[id] = rt

	return nil
}

// ConfirmFix cancels the rollback timer for the most recent fix.
func (b *Budget) ConfirmFix(fixID string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if rt, ok := b.timers[fixID]; ok {
		rt.timer.Stop()
		rt.cancel()
		delete(b.timers, fixID)
	}
}

// LastFixID returns the ID of the most recently recorded fix.
func (b *Budget) LastFixID() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.count == 0 {
		return ""
	}
	return fmt.Sprintf("hl-%s-%d", b.sessionID, b.count)
}

// Close stops all pending rollback timers.
func (b *Budget) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, rt := range b.timers {
		rt.timer.Stop()
		rt.cancel()
		delete(b.timers, id)
	}
}

func rollbackFiles(worktreePath string, files []string) {
	if len(files) == 0 {
		return
	}
	args := append([]string{"checkout", "--"}, files...)
	cmd := exec.Command("git", args...)
	cmd.Dir = worktreePath
	_ = cmd.Run()
}
