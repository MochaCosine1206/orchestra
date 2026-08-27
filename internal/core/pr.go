package core

import (
	"encoding/json"
	"fmt"
)

// CommandRunner executes external commands and returns stdout and any error.
// Inject a mock implementation in tests to avoid real gh CLI calls.
type CommandRunner interface {
	Run(name string, args ...string) (string, error)
}

// PRRequest contains parameters for creating a pull request.
type PRRequest struct {
	Repo       string // owner/repo format
	Branch     string // head branch
	BaseBranch string // base branch
	Title      string
	Body       string
	Labels     []string
	Draft      bool
}

// GHPRResult contains the outcome of a PR operation.
// Named GHPRResult to avoid collision with orchestrator.PRResult.
type GHPRResult struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
}

// PRClient manages GitHub pull requests via the gh CLI.
type PRClient struct {
	Runner CommandRunner
}

// NewPRClient creates a PRClient with the given command runner.
func NewPRClient(runner CommandRunner) *PRClient {
	return &PRClient{Runner: runner}
}

// CreatePR creates a pull request and returns the result.
func (c *PRClient) CreatePR(req PRRequest) (*GHPRResult, error) {
	args := []string{"pr", "create",
		"--repo", req.Repo,
		"--title", req.Title,
		"--body", req.Body,
		"--head", req.Branch,
		"--base", req.BaseBranch,
		"--json", "number,url,state",
	}
	for _, l := range req.Labels {
		args = append(args, "--label", l)
	}
	if req.Draft {
		args = append(args, "--draft")
	}

	out, err := c.Runner.Run("gh", args...)
	if err != nil {
		return nil, fmt.Errorf("creating PR: %w", err)
	}

	var result GHPRResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return nil, fmt.Errorf("parsing PR response: %w", err)
	}
	return &result, nil
}

// ListPRs lists pull requests for a repository filtered by state.
func (c *PRClient) ListPRs(repo, state string) ([]GHPRResult, error) {
	args := []string{"pr", "list",
		"--repo", repo,
		"--state", state,
		"--json", "number,url,state",
	}

	out, err := c.Runner.Run("gh", args...)
	if err != nil {
		return nil, fmt.Errorf("listing PRs: %w", err)
	}

	var results []GHPRResult
	if err := json.Unmarshal([]byte(out), &results); err != nil {
		return nil, fmt.Errorf("parsing PR list: %w", err)
	}
	return results, nil
}

// MergePR merges a pull request by number using the specified method (squash, merge, or rebase).
func (c *PRClient) MergePR(repo string, number int, method string) error {
	if method == "" {
		method = "squash"
	}
	args := []string{"pr", "merge",
		"--repo", repo,
		fmt.Sprintf("%d", number),
		"--" + method,
	}

	_, err := c.Runner.Run("gh", args...)
	if err != nil {
		return fmt.Errorf("merging PR #%d: %w", number, err)
	}
	return nil
}

// CheckPRStatus returns the current status of a pull request.
func (c *PRClient) CheckPRStatus(repo string, number int) (*GHPRResult, error) {
	args := []string{"pr", "view",
		"--repo", repo,
		fmt.Sprintf("%d", number),
		"--json", "number,url,state",
	}

	out, err := c.Runner.Run("gh", args...)
	if err != nil {
		return nil, fmt.Errorf("checking PR #%d: %w", number, err)
	}

	var result GHPRResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return nil, fmt.Errorf("parsing PR status: %w", err)
	}
	return &result, nil
}
