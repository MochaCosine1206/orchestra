package bridge

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
)

// gitHasChanges returns true if there are uncommitted/untracked changes.
func gitHasChanges(dir string) bool {
	// Check staged + unstaged
	cmd := exec.Command("git", "diff", "--quiet", "HEAD")
	cmd.Dir = dir
	if err := cmd.Run(); err != nil {
		return true
	}
	// Check untracked
	cmd = exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// ghPRURL returns the URL of an existing PR for the current branch, or empty.
func ghPRURL(dir string) string {
	cmd := exec.Command("gh", "pr", "view", "--json", "url", "-q", ".url")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// HandleShip commits, pushes, and creates a PR from a feature branch.
func (b *Bridge) HandleShip(ctx context.Context, topicID int64, slug, dir string) {
	branch := gitBranch(dir)
	if branch == "" || branch == "main" || branch == "master" || branch == "dev" {
		b.tg.SendMessage(ctx, fmt.Sprintf("Cannot ship from <b>%s</b>. Switch to a feature branch first.", EscapeHTML(branch)), topicID, "HTML")
		return
	}

	b.tg.SendMessage(ctx, fmt.Sprintf("Shipping <b>%s</b>...", EscapeHTML(branch)), topicID, "HTML")

	// Build commit message from last-summary
	commitMsg := "Ship " + branch
	if summary, ok := ReadLastSummary(b.cfg.StateBase, slug); ok {
		lines := strings.SplitN(summary, "\n", 20)
		if len(lines) > 20 {
			lines = lines[:20]
		}
		msg := strings.Join(lines, "\n")
		if len(msg) > 500 {
			msg = msg[:500]
		}
		commitMsg = msg
	}

	// Stage + commit if there are changes
	if gitHasChanges(dir) {
		if out, err := gitExec(dir, "add", "-A"); err != nil {
			b.tg.SendMessage(ctx, "git add failed: "+EscapeHTML(out), topicID, "HTML")
			return
		}
		if out, err := gitExec(dir, "commit", "-m", commitMsg); err != nil {
			b.tg.SendMessage(ctx, "git commit failed: "+EscapeHTML(out), topicID, "HTML")
			return
		}
	}

	// Push
	if out, err := gitExec(dir, "push", "-u", "origin", branch); err != nil {
		b.tg.SendMessage(ctx, "git push failed: "+EscapeHTML(out), topicID, "HTML")
		return
	}

	// Create PR if none exists
	prURL := ghPRURL(dir)
	if prURL == "" {
		prTitle := BranchToTaskName(branch)
		if prTitle == "" {
			prTitle = branch
		}
		prBody := "Shipped from Telegram /ship command."
		if summary, ok := ReadLastSummary(b.cfg.StateBase, slug); ok {
			prBody = summary
		}
		cmd := exec.Command("gh", "pr", "create", "--title", prTitle, "--body", prBody, "--base", "dev")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			b.tg.SendMessage(ctx, "gh pr create failed: "+EscapeHTML(string(out)), topicID, "HTML")
			return
		}
		// Extract URL from output
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "github.com") {
				prURL = strings.TrimSpace(line)
				break
			}
		}
	}

	// Send success with Merge button
	mergeKB := actionKB("\U0001f500 Merge", "merge:"+slug)
	text := fmt.Sprintf("Shipped! PR: %s\n\n<i>Reply within %ds to continue...</i>",
		EscapeHTML(prURL), int(b.cfg.ReplyTimeout.Seconds()))
	b.tg.SendWithKeyboard(ctx, text, mergeKB, topicID, "HTML")
}

// HandleMerge merges an existing PR via gh.
func (b *Bridge) HandleMerge(ctx context.Context, topicID int64, slug, dir string) {
	branch := gitBranch(dir)
	b.tg.SendMessage(ctx, fmt.Sprintf("Merging PR for <b>%s</b>...", EscapeHTML(branch)), topicID, "HTML")

	prURL := ghPRURL(dir)
	if prURL == "" {
		b.tg.SendMessage(ctx, fmt.Sprintf("No open PR found for <b>%s</b>.", EscapeHTML(branch)), topicID, "HTML")
		return
	}

	if out, err := ghExec(dir, "pr", "merge", "--squash", "--delete-branch"); err != nil {
		b.tg.SendMessage(ctx, "Merge failed: "+EscapeHTML(out), topicID, "HTML")
		return
	}

	// Switch to dev and sync
	if out, err := gitExec(dir, "checkout", "dev"); err != nil {
		b.tg.SendMessage(ctx, "Merged but checkout dev failed: "+EscapeHTML(out), topicID, "HTML")
		return
	}
	gitExec(dir, "fetch", "origin", "dev")
	if out, err := gitExec(dir, "pull", "--rebase", "origin", "dev"); err != nil {
		// Abort rebase and try hard reset
		gitExec(dir, "rebase", "--abort")
		if out2, err2 := gitExec(dir, "reset", "--hard", "origin/dev"); err2 != nil {
			b.tg.SendMessage(ctx, "Merged but sync failed: "+EscapeHTML(out+" / "+out2), topicID, "HTML")
			return
		}
		_ = out
	}

	summaryText := ""
	if summary, ok := ReadLastSummary(b.cfg.StateBase, slug); ok {
		summaryText = "\n\n" + EscapeHTML(summary)
	}
	text := fmt.Sprintf("Merged <b>%s</b> \u2192 dev. Checked out dev and pulled.%s\n\n<i>Reply within %ds to continue...</i>",
		EscapeHTML(branch), summaryText, int(b.cfg.ReplyTimeout.Seconds()))
	b.tg.SendMessage(ctx, text, topicID, "HTML")
}

// HandlePush commits and pushes on trunk branches.
func (b *Bridge) HandlePush(ctx context.Context, topicID int64, slug, dir string) {
	branch := gitBranch(dir)
	b.tg.SendMessage(ctx, fmt.Sprintf("Pushing <b>%s</b>...", EscapeHTML(branch)), topicID, "HTML")

	if !gitHasChanges(dir) {
		b.tg.SendMessage(ctx, "Nothing to push \u2014 working tree clean.", topicID, "HTML")
		return
	}

	commitMsg := "Update " + branch
	if summary, ok := ReadLastSummary(b.cfg.StateBase, slug); ok {
		lines := strings.SplitN(summary, "\n", 20)
		if len(lines) > 20 {
			lines = lines[:20]
		}
		msg := strings.Join(lines, "\n")
		if len(msg) > 500 {
			msg = msg[:500]
		}
		commitMsg = msg
	}

	if out, err := gitExec(dir, "add", "-A"); err != nil {
		b.tg.SendMessage(ctx, "git add failed: "+EscapeHTML(out), topicID, "HTML")
		return
	}
	if out, err := gitExec(dir, "commit", "-m", commitMsg); err != nil {
		b.tg.SendMessage(ctx, "git commit failed: "+EscapeHTML(out), topicID, "HTML")
		return
	}
	if out, err := gitExec(dir, "push"); err != nil {
		b.tg.SendMessage(ctx, "git push failed: "+EscapeHTML(out), topicID, "HTML")
		return
	}

	summaryText := ""
	if summary, ok := ReadLastSummary(b.cfg.StateBase, slug); ok {
		summaryText = "\n\n" + EscapeHTML(summary)
	}
	text := fmt.Sprintf("Pushed to <b>%s</b>.%s\n\n<i>Reply within %ds to continue...</i>",
		EscapeHTML(branch), summaryText, int(b.cfg.ReplyTimeout.Seconds()))
	b.tg.SendMessage(ctx, text, topicID, "HTML")
}

// HandlePR creates a PR without committing (already pushed).
func (b *Bridge) HandlePR(ctx context.Context, topicID int64, slug, dir string) {
	branch := gitBranch(dir)
	b.tg.SendMessage(ctx, fmt.Sprintf("Creating PR for <b>%s</b>...", EscapeHTML(branch)), topicID, "HTML")

	existing := ghPRURL(dir)
	if existing != "" {
		b.tg.SendMessage(ctx, "PR already exists: "+EscapeHTML(existing), topicID, "HTML")
		return
	}

	prTitle := BranchToTaskName(branch)
	if prTitle == "" {
		prTitle = branch
	}
	prBody := "Created from Telegram."
	if summary, ok := ReadLastSummary(b.cfg.StateBase, slug); ok {
		prBody = summary
	}

	cmd := exec.Command("gh", "pr", "create", "--title", prTitle, "--body", prBody, "--base", "dev")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.tg.SendMessage(ctx, "gh pr create failed: "+EscapeHTML(string(out)), topicID, "HTML")
		return
	}

	prURL := ""
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "github.com") {
			prURL = strings.TrimSpace(line)
			break
		}
	}
	text := fmt.Sprintf("PR created: %s\n\n<i>Reply within %ds to continue...</i>",
		EscapeHTML(prURL), int(b.cfg.ReplyTimeout.Seconds()))
	b.tg.SendMessage(ctx, text, topicID, "HTML")
}

// ActionLabel returns the gerund label for callback acknowledgment.
func ActionLabel(action string) string {
	switch action {
	case "ship":
		return "Shipping..."
	case "merge":
		return "Merging..."
	case "push":
		return "Pushing..."
	case "pr":
		return "Creating PR..."
	default:
		return titleCase(action) + "ing..."
	}
}

func gitExec(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("git command failed", "cmd", strings.Join(args, " "), "dir", dir, "output", string(out))
	}
	return string(out), err
}

func ghExec(dir string, args ...string) (string, error) {
	cmd := exec.Command("gh", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		slog.Error("gh command failed", "cmd", strings.Join(args, " "), "dir", dir, "output", string(out))
	}
	return string(out), err
}
