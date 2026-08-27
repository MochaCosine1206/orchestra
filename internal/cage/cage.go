// Package cage implements hard-limit enforcement (the "cage pattern") for
// Claude Orchestra agents. Every operation that could exceed safety bounds
// is checked against CageConfig before execution, and violations are logged
// to SQLite for audit.
package cage

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

// CageConfig defines the hard limits enforced by the cage.
type CageConfig struct {
	// MaxRepos is the maximum number of distinct repos an agent may access.
	MaxRepos int
	// AllowedBranches restricts which branch names agents may create or push to.
	AllowedBranches []string
	// MaxFileOps is the maximum cumulative file operations allowed per session.
	MaxFileOps int
	// MaxSpendTokens is the maximum token spend allowed per session.
	MaxSpendTokens int64
}

// DefaultConfig returns a CageConfig with sensible defaults.
func DefaultConfig() CageConfig {
	return CageConfig{
		MaxRepos:        3,
		AllowedBranches: []string{"feature/*", "fix/*", "experiment/*"},
		MaxFileOps:      500,
		MaxSpendTokens:  2_000_000,
	}
}

// ViolationType classifies cage violations.
type ViolationType string

const (
	ViolationRepo    ViolationType = "repo_access"
	ViolationBranch  ViolationType = "branch_access"
	ViolationFileOps ViolationType = "file_ops"
	ViolationSpend   ViolationType = "token_spend"
)

// CageEnforcer checks operations against the configured hard limits and logs
// violations to SQLite.
type CageEnforcer struct {
	config CageConfig
	db     *sql.DB
	logger *slog.Logger

	mu         sync.Mutex
	knownRepos map[string]struct{}
	fileOps    int
	spend      int64
}

// NewCageEnforcer creates an enforcer with the given config and database.
func NewCageEnforcer(config CageConfig, db *sql.DB, logger *slog.Logger) *CageEnforcer {
	return &CageEnforcer{
		config:     config,
		db:         db,
		logger:     logger,
		knownRepos: make(map[string]struct{}),
	}
}

// ensureCageViolationsTable creates the cage_violations table if it does not
// exist.
func ensureCageViolationsTable(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS cage_violations (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			violation_type TEXT NOT NULL,
			detail         TEXT NOT NULL,
			created_at     DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("creating cage_violations table: %w", err)
	}
	return nil
}

// Init ensures the backing SQLite table exists.
func (ce *CageEnforcer) Init(ctx context.Context) error {
	return ensureCageViolationsTable(ctx, ce.db)
}

// CheckRepoAccess returns (true, "") if the repo path is allowed, or
// (false, reason) if adding it would exceed MaxRepos.
func (ce *CageEnforcer) CheckRepoAccess(ctx context.Context, path string) (bool, string) {
	cleaned := filepath.Clean(path)

	ce.mu.Lock()
	defer ce.mu.Unlock()

	if _, ok := ce.knownRepos[cleaned]; ok {
		return true, ""
	}

	if len(ce.knownRepos) >= ce.config.MaxRepos {
		reason := fmt.Sprintf("repo limit exceeded: %d/%d (attempted %s)", len(ce.knownRepos), ce.config.MaxRepos, cleaned)
		ce.logViolation(ctx, ViolationRepo, reason)
		return false, reason
	}

	ce.knownRepos[cleaned] = struct{}{}
	return true, ""
}

// CheckBranchAccess returns (true, "") if the branch name matches one of the
// AllowedBranches patterns, or (false, reason) otherwise.
func (ce *CageEnforcer) CheckBranchAccess(ctx context.Context, branch string) (bool, string) {
	for _, pattern := range ce.config.AllowedBranches {
		if matchBranchPattern(pattern, branch) {
			return true, ""
		}
	}

	reason := fmt.Sprintf("branch %q not in allowed list %v", branch, ce.config.AllowedBranches)
	ce.logViolation(ctx, ViolationBranch, reason)
	return false, reason
}

// CheckFileOps returns (true, "") if adding count file operations would stay
// within MaxFileOps, or (false, reason) otherwise.
func (ce *CageEnforcer) CheckFileOps(ctx context.Context, count int) (bool, string) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if ce.fileOps+count > ce.config.MaxFileOps {
		reason := fmt.Sprintf("file ops limit exceeded: %d+%d > %d", ce.fileOps, count, ce.config.MaxFileOps)
		ce.logViolation(ctx, ViolationFileOps, reason)
		return false, reason
	}

	ce.fileOps += count
	return true, ""
}

// CheckSpend returns (true, "") if adding tokens would stay within
// MaxSpendTokens, or (false, reason) otherwise.
func (ce *CageEnforcer) CheckSpend(ctx context.Context, tokens int64) (bool, string) {
	ce.mu.Lock()
	defer ce.mu.Unlock()

	if ce.spend+tokens > ce.config.MaxSpendTokens {
		reason := fmt.Sprintf("token spend limit exceeded: %d+%d > %d", ce.spend, tokens, ce.config.MaxSpendTokens)
		ce.logViolation(ctx, ViolationSpend, reason)
		return false, reason
	}

	ce.spend += tokens
	return true, ""
}

// logViolation writes a violation to SQLite. Errors are logged but not returned
// to avoid blocking the check path.
func (ce *CageEnforcer) logViolation(ctx context.Context, vtype ViolationType, detail string) {
	ce.logger.Warn("cage violation", "type", string(vtype), "detail", detail)
	_, err := ce.db.ExecContext(ctx,
		`INSERT INTO cage_violations (violation_type, detail) VALUES (?, ?)`,
		string(vtype), detail)
	if err != nil {
		ce.logger.Error("recording cage violation", "error", err)
	}
}

// matchBranchPattern matches a branch name against a pattern with simple
// wildcard support (e.g. "feature/*" matches "feature/foo").
func matchBranchPattern(pattern, branch string) bool {
	if !strings.Contains(pattern, "*") {
		return pattern == branch
	}
	prefix := strings.TrimSuffix(pattern, "*")
	return strings.HasPrefix(branch, prefix)
}
