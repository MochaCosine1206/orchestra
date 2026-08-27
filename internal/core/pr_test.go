package core

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// mockRunner implements CommandRunner for testing.
type mockRunner struct {
	calls    []mockCall
	response string
	err      error
}

type mockCall struct {
	name string
	args []string
}

func (m *mockRunner) Run(name string, args ...string) (string, error) {
	m.calls = append(m.calls, mockCall{name: name, args: args})
	return m.response, m.err
}

func TestCreatePR(t *testing.T) {
	t.Parallel()
	expected := GHPRResult{Number: 42, URL: "https://github.com/org/repo/pull/42", State: "OPEN"}
	respJSON, _ := json.Marshal(expected)

	runner := &mockRunner{response: string(respJSON)}
	client := NewPRClient(runner)

	result, err := client.CreatePR(PRRequest{
		Repo:       "org/repo",
		Branch:     "feature-branch",
		BaseBranch: "main",
		Title:      "Test PR",
		Body:       "Test body",
		Labels:     []string{"bug", "urgent"},
		Draft:      true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Number != 42 {
		t.Errorf("expected PR #42, got #%d", result.Number)
	}
	if result.URL != expected.URL {
		t.Errorf("expected URL %q, got %q", expected.URL, result.URL)
	}

	// Verify gh CLI args
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(runner.calls))
	}
	call := runner.calls[0]
	if call.name != "gh" {
		t.Errorf("expected command 'gh', got %q", call.name)
	}
	args := strings.Join(call.args, " ")
	if !strings.Contains(args, "--repo org/repo") {
		t.Error("missing --repo flag")
	}
	if !strings.Contains(args, "--draft") {
		t.Error("missing --draft flag")
	}
	if !strings.Contains(args, "--label bug") {
		t.Error("missing --label bug")
	}
}

func TestCreatePR_Error(t *testing.T) {
	t.Parallel()
	runner := &mockRunner{err: fmt.Errorf("gh: not authenticated")}
	client := NewPRClient(runner)

	_, err := client.CreatePR(PRRequest{Repo: "org/repo", Branch: "b", BaseBranch: "main", Title: "t", Body: "b"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "creating PR") {
		t.Errorf("expected wrapped error, got: %v", err)
	}
}

func TestListPRs(t *testing.T) {
	t.Parallel()
	prs := []GHPRResult{
		{Number: 1, URL: "https://github.com/org/repo/pull/1", State: "OPEN"},
		{Number: 2, URL: "https://github.com/org/repo/pull/2", State: "OPEN"},
	}
	respJSON, _ := json.Marshal(prs)

	runner := &mockRunner{response: string(respJSON)}
	client := NewPRClient(runner)

	results, err := client.ListPRs("org/repo", "open")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Errorf("expected 2 PRs, got %d", len(results))
	}
}

func TestMergePR(t *testing.T) {
	t.Parallel()
	runner := &mockRunner{response: ""}
	client := NewPRClient(runner)

	err := client.MergePR("org/repo", 42, "squash")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := strings.Join(runner.calls[0].args, " ")
	if !strings.Contains(args, "--squash") {
		t.Error("missing --squash flag")
	}
	if !strings.Contains(args, "--repo org/repo") {
		t.Error("missing --repo flag")
	}
}

func TestMergePR_DefaultMethod(t *testing.T) {
	t.Parallel()
	runner := &mockRunner{response: ""}
	client := NewPRClient(runner)

	err := client.MergePR("org/repo", 1, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	args := strings.Join(runner.calls[0].args, " ")
	if !strings.Contains(args, "--squash") {
		t.Error("expected default squash method")
	}
}

func TestCheckPRStatus(t *testing.T) {
	t.Parallel()
	expected := GHPRResult{Number: 10, URL: "https://github.com/org/repo/pull/10", State: "MERGED"}
	respJSON, _ := json.Marshal(expected)

	runner := &mockRunner{response: string(respJSON)}
	client := NewPRClient(runner)

	result, err := client.CheckPRStatus("org/repo", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.State != "MERGED" {
		t.Errorf("expected state MERGED, got %q", result.State)
	}
}

func TestCheckPRStatus_NotFound(t *testing.T) {
	t.Parallel()
	runner := &mockRunner{err: fmt.Errorf("gh: Could not resolve to a PullRequest")}
	client := NewPRClient(runner)

	_, err := client.CheckPRStatus("org/repo", 999)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "checking PR #999") {
		t.Errorf("expected wrapped error with PR number, got: %v", err)
	}
}
