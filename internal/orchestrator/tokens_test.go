package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func TestTokens_EmptyDB(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	ctx := context.Background()

	summary, err := c.Tokens(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Total.TotalTokens != 0 {
		t.Errorf("expected zero tokens, got %d", summary.Total.TotalTokens)
	}
	if len(summary.ByRole) != 0 {
		t.Errorf("expected empty by-role, got %d entries", len(summary.ByRole))
	}
}

func TestTokens_NoLogsDir(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	// Don't create logs/ dir
	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	ctx := context.Background()

	summary, err := c.Tokens(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if summary.Total.TotalTokens != 0 {
		t.Errorf("expected zero tokens, got %d", summary.Total.TotalTokens)
	}
}

func TestTokens_ParsesJSONL(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	logsDir := filepath.Join(repoRoot, ".orchestra", "logs")
	os.MkdirAll(logsDir, 0o755)

	// Register an agent with a task
	ctx := context.Background()
	d.RegisterAgent(ctx, db.Agent{ID: "a-001", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "T-001", Title: "test", Status: "running", Role: "implementer"})
	d.ExecContext(ctx, `UPDATE agents SET current_task = 'T-001' WHERE id = 'a-001'`)

	// Write a JSONL log with usage data
	logContent := `{"type":"message","usage":{"input_tokens":100,"output_tokens":50,"cache_read_input_tokens":20}}
{"type":"message","usage":{"input_tokens":200,"output_tokens":100,"cache_read_input_tokens":30}}
`
	os.WriteFile(filepath.Join(logsDir, "T-001.jsonl"), []byte(logContent), 0o644)

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	summary, err := c.Tokens(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if summary.Total.InputTokens != 300 {
		t.Errorf("total input = %d, want 300", summary.Total.InputTokens)
	}
	if summary.Total.OutputTokens != 150 {
		t.Errorf("total output = %d, want 150", summary.Total.OutputTokens)
	}
	if summary.Total.CacheRead != 50 {
		t.Errorf("total cache_read = %d, want 50", summary.Total.CacheRead)
	}
	if summary.Total.TotalTokens != 450 {
		t.Errorf("total = %d, want 450", summary.Total.TotalTokens)
	}
}

func TestTokens_RoleGrouping(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	logsDir := filepath.Join(repoRoot, ".orchestra", "logs")
	os.MkdirAll(logsDir, 0o755)

	ctx := context.Background()
	// Two agents with different roles
	d.RegisterAgent(ctx, db.Agent{ID: "a-001", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "T-001", Title: "impl", Status: "running", Role: "implementer"})
	d.ExecContext(ctx, `UPDATE agents SET current_task = 'T-001' WHERE id = 'a-001'`)

	d.RegisterAgent(ctx, db.Agent{ID: "a-002", Role: "researcher", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "T-002", Title: "research", Status: "running", Role: "researcher"})
	d.ExecContext(ctx, `UPDATE agents SET current_task = 'T-002' WHERE id = 'a-002'`)

	os.WriteFile(filepath.Join(logsDir, "T-001.jsonl"),
		[]byte(`{"type":"message","usage":{"input_tokens":100,"output_tokens":50}}`+"\n"), 0o644)
	os.WriteFile(filepath.Join(logsDir, "T-002.jsonl"),
		[]byte(`{"type":"message","usage":{"input_tokens":200,"output_tokens":100}}`+"\n"), 0o644)

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	summary, err := c.Tokens(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(summary.ByRole) != 2 {
		t.Errorf("expected 2 roles, got %d", len(summary.ByRole))
	}

	impl, ok := summary.ByRole["implementer"]
	if !ok {
		t.Fatal("missing implementer role")
	}
	if impl.InputTokens != 100 {
		t.Errorf("implementer input = %d, want 100", impl.InputTokens)
	}

	res, ok := summary.ByRole["researcher"]
	if !ok {
		t.Fatal("missing researcher role")
	}
	if res.InputTokens != 200 {
		t.Errorf("researcher input = %d, want 200", res.InputTokens)
	}
}

func TestTokens_MultipleAgentsAggregate(t *testing.T) {
	d := setupTestDB(t)
	repoRoot := t.TempDir()
	logsDir := filepath.Join(repoRoot, ".orchestra", "logs")
	os.MkdirAll(logsDir, 0o755)

	ctx := context.Background()
	// Two tasks, same role
	d.RegisterAgent(ctx, db.Agent{ID: "a-001", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "T-001", Title: "impl1", Status: "running", Role: "implementer"})
	d.ExecContext(ctx, `UPDATE agents SET current_task = 'T-001' WHERE id = 'a-001'`)

	d.RegisterAgent(ctx, db.Agent{ID: "a-002", Role: "implementer", Status: "working"})
	d.CreateTask(ctx, db.Task{ID: "T-002", Title: "impl2", Status: "running", Role: "implementer"})
	d.ExecContext(ctx, `UPDATE agents SET current_task = 'T-002' WHERE id = 'a-002'`)

	os.WriteFile(filepath.Join(logsDir, "T-001.jsonl"),
		[]byte(`{"type":"message","usage":{"input_tokens":100,"output_tokens":50}}`+"\n"), 0o644)
	os.WriteFile(filepath.Join(logsDir, "T-002.jsonl"),
		[]byte(`{"type":"message","usage":{"input_tokens":200,"output_tokens":100}}`+"\n"), 0o644)

	c, _ := New(ConductorOpts{DB: d, RepoRoot: repoRoot})
	summary, err := c.Tokens(ctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	impl := summary.ByRole["implementer"]
	if impl.InputTokens != 300 {
		t.Errorf("aggregated input = %d, want 300", impl.InputTokens)
	}
	if len(summary.Agents) != 2 {
		t.Errorf("expected 2 agents, got %d", len(summary.Agents))
	}
}

func TestFormatTokenSummary(t *testing.T) {
	summary := &TokenSummary{
		ByRole: map[string]TokenUsage{
			"implementer": {Role: "implementer", InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
		Total: TokenUsage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		Agents: []TokenUsage{
			{TaskID: "T-001", Role: "implementer", InputTokens: 100, OutputTokens: 50, TotalTokens: 150},
		},
	}

	output := FormatTokenSummary(summary)
	if !strings.Contains(output, "Token Usage Summary") {
		t.Error("output should contain header")
	}
	if !strings.Contains(output, "implementer") {
		t.Error("output should contain role")
	}
	if !strings.Contains(output, "TOTAL") {
		t.Error("output should contain total row")
	}
	if !strings.Contains(output, "T-001") {
		t.Error("output should contain per-agent breakdown")
	}
}

func TestFormatTokenSummary_Empty(t *testing.T) {
	summary := &TokenSummary{
		ByRole: make(map[string]TokenUsage),
	}
	output := FormatTokenSummary(summary)
	if !strings.Contains(output, "Token Usage Summary") {
		t.Error("empty summary should still have header")
	}
}

func TestParseTokensFromLog_InvalidJSON(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.jsonl")
	os.WriteFile(logFile, []byte("not json\n{bad\n"), 0o644)

	usage := parseTokensFromLog(logFile, "T-001", "a-001", "implementer")
	if usage.TotalTokens != 0 {
		t.Errorf("expected 0 tokens for invalid JSON, got %d", usage.TotalTokens)
	}
}

func TestParseTokensFromLog_NoUsageField(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "test.jsonl")
	os.WriteFile(logFile, []byte(`{"type":"message","content":"hello"}`+"\n"), 0o644)

	usage := parseTokensFromLog(logFile, "T-001", "a-001", "implementer")
	if usage.TotalTokens != 0 {
		t.Errorf("expected 0 tokens for no usage field, got %d", usage.TotalTokens)
	}
}
