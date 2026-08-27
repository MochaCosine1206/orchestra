package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

func setupStatusTestDB(t *testing.T) (string, *db.DB) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	d, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("opening db: %v", err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return dbPath, d
}

func TestStatusDisplayEmptyDB(t *testing.T) {
	dbPath, d := setupStatusTestDB(t)
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"status", "--db", dbPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("status failed: %v", err)
	}

	out := buf.String()
	for _, want := range []string{
		"CLAUDE ORCHESTRA STATUS",
		"AGENT STATUS",
		"TASK PROGRESS",
		"FILE LOCKS",
		"RECENT EVENTS",
		"PROCESS STATUS",
		"(no agents)",
		"(no tasks)",
		"(no active locks)",
		"(no events)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output should contain %q", want)
		}
	}
}

func TestStatusDisplayWithData(t *testing.T) {
	dbPath, d := setupStatusTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO agents (id, role, status) VALUES ('a1', 'implementer', 'active')`)
	if err != nil {
		t.Fatalf("inserting agent: %v", err)
	}
	_, err = d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES
		 ('t1', 'Build auth module', 'running', 'implementer'),
		 ('t2', 'Scout dependencies', 'done', 'scout')`)
	if err != nil {
		t.Fatalf("inserting tasks: %v", err)
	}
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"status", "--db", dbPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("status failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "implementer") {
		t.Error("output should show agent role")
	}
	if !strings.Contains(out, "Build auth module") {
		t.Error("output should show task title")
	}
	if !strings.Contains(out, "1 running") {
		t.Error("output should show running task count")
	}
	if !strings.Contains(out, "1 done") {
		t.Error("output should show done task count")
	}
}

func TestStatusJSONEmptyDB(t *testing.T) {
	dbPath, d := setupStatusTestDB(t)
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"status", "--json", "--db", dbPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("status --json failed: %v", err)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v\noutput: %s", err, buf.String())
	}

	for _, key := range []string{"tasks", "agents", "locks", "events", "summary"} {
		if _, ok := result[key]; !ok {
			t.Errorf("JSON missing key: %s", key)
		}
	}
}

func TestStatusJSONWithData(t *testing.T) {
	dbPath, d := setupStatusTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO tasks (id, title, status, role) VALUES
		 ('t1', 'Test task', 'pending', 'implementer')`)
	if err != nil {
		t.Fatalf("inserting task: %v", err)
	}
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"status", "--json", "--db", dbPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("status --json failed: %v", err)
	}

	var result db.StatusJSON
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(result.Tasks) != 1 {
		t.Errorf("expected 1 task in JSON, got %d", len(result.Tasks))
	}
	if result.Summary.TaskCounts["pending"] != 1 {
		t.Errorf("expected 1 pending in summary, got %d", result.Summary.TaskCounts["pending"])
	}
}

func TestStatusMissingDB(t *testing.T) {
	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetErr(buf)
	root.SetArgs([]string{"status", "--db", "/tmp/nonexistent-orchestra-test.db"})

	err := root.Execute()
	if err == nil {
		t.Error("status should fail with missing database")
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		input string
		max   int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 8, "hello..."},
		{"hi", 2, "hi"},
		{"abc", 3, "abc"},
		{"abcd", 3, "abc"},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := truncate(tc.input, tc.max)
			if got != tc.want {
				t.Errorf("truncate(%q, %d) = %q, want %q", tc.input, tc.max, got, tc.want)
			}
		})
	}
}

func TestFormatDuration(t *testing.T) {
	cases := []struct {
		name string
		dur  time.Duration
		want string
	}{
		{"seconds", 30 * time.Second, "30s"},
		{"minutes", 2*time.Minute + 5*time.Second, "2m 5s"},
		{"hours", 1*time.Hour + 2*time.Minute + 5*time.Second, "1h 2m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := formatDuration(tc.dur)
			if got != tc.want {
				t.Errorf("formatDuration(%v) = %q, want %q", tc.dur, got, tc.want)
			}
		})
	}
}

func TestStatusViolationsDisplayEmpty(t *testing.T) {
	dbPath, d := setupStatusTestDB(t)
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"status", "--violations", "--db", dbPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("status --violations failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "FILE VIOLATIONS") {
		t.Error("output should contain FILE VIOLATIONS section")
	}
	if !strings.Contains(out, "(no violations)") {
		t.Error("output should show (no violations) when none exist")
	}
}

func TestStatusViolationsDisplayWithData(t *testing.T) {
	dbPath, d := setupStatusTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO events (timestamp, agent_id, task_id, event_type, payload) VALUES
		 (datetime('now'), 'a1', 't1', 'file_violation', '{"file_path":"internal/cmd/status.go","task_id":"t1"}'),
		 (datetime('now'), 'a2', 't2', 'task_started', '{}')`)
	if err != nil {
		t.Fatalf("inserting events: %v", err)
	}
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"status", "--violations", "--db", dbPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("status --violations failed: %v", err)
	}

	out := buf.String()
	if !strings.Contains(out, "FILE VIOLATIONS") {
		t.Error("output should contain FILE VIOLATIONS section")
	}
	if !strings.Contains(out, "internal/cmd/status.go") {
		t.Error("output should show the violated file path")
	}
	if strings.Contains(out, "(no violations)") {
		t.Error("output should not show (no violations) when violations exist")
	}
}

func TestStatusViolationsNotShownByDefault(t *testing.T) {
	dbPath, d := setupStatusTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO events (timestamp, agent_id, task_id, event_type, payload) VALUES
		 (datetime('now'), 'a1', 't1', 'file_violation', '{"file_path":"foo.go"}')`)
	if err != nil {
		t.Fatalf("inserting events: %v", err)
	}
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"status", "--db", dbPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("status failed: %v", err)
	}

	if strings.Contains(buf.String(), "FILE VIOLATIONS") {
		t.Error("FILE VIOLATIONS section should not appear without --violations flag")
	}
}

func TestStatusViolationsJSON(t *testing.T) {
	dbPath, d := setupStatusTestDB(t)
	ctx := context.Background()

	_, err := d.ExecContext(ctx,
		`INSERT INTO events (timestamp, agent_id, task_id, event_type, payload) VALUES
		 (datetime('now'), 'a1', 't1', 'file_violation', '{"file_path":"main.go"}'),
		 (datetime('now'), 'a2', 't2', 'task_started', '{}')`)
	if err != nil {
		t.Fatalf("inserting events: %v", err)
	}
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"status", "--json", "--violations", "--db", dbPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("status --json --violations failed: %v", err)
	}

	var result db.StatusJSON
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if len(result.Violations) != 1 {
		t.Errorf("expected 1 violation, got %d", len(result.Violations))
	}
	if len(result.Violations) > 0 && result.Violations[0].EventType != "file_violation" {
		t.Errorf("expected file_violation event type, got %s", result.Violations[0].EventType)
	}
}

func TestStatusViolationsJSONOmittedWithoutFlag(t *testing.T) {
	dbPath, d := setupStatusTestDB(t)
	d.Close()

	root := NewRootCmd()
	buf := new(bytes.Buffer)
	root.SetOut(buf)
	root.SetArgs([]string{"status", "--json", "--db", dbPath})

	if err := root.Execute(); err != nil {
		t.Fatalf("status --json failed: %v", err)
	}

	var raw map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &raw); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if _, ok := raw["violations"]; ok {
		t.Error("violations key should be omitted from JSON when --violations flag is not set")
	}
}

func TestProcessStatusMonitorStopped(t *testing.T) {
	tmpDir := t.TempDir()
	origDir, _ := os.Getwd()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	d, err := db.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	if err := d.InitSchema(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	buf := new(bytes.Buffer)
	printProcessStatus(buf, d, context.Background())

	if !strings.Contains(buf.String(), "STOPPED") {
		t.Errorf("expected STOPPED, got: %s", buf.String())
	}
}
