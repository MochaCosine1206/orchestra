package bridge

import (
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ProjectSlug derives a filesystem-safe slug from a directory path.
// Matches the bash: basename | lowercase | spaces→hyphens | strip non-safe.
func ProjectSlug(cwd string) string {
	base := filepath.Base(cwd)
	base = strings.ToLower(base)
	base = strings.ReplaceAll(base, " ", "-")
	var b strings.Builder
	for _, r := range base {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// EnsureStateDir creates the project state directory structure and returns the path.
func EnsureStateDir(stateBase, slug string) string {
	dir := filepath.Join(stateBase, slug)
	os.MkdirAll(filepath.Join(dir, "sessions"), 0755)
	os.MkdirAll(filepath.Join(dir, "telegram"), 0755)
	return dir
}

// StateDir returns the state directory path without creating it.
func StateDir(stateBase, slug string) string {
	return filepath.Join(stateBase, slug)
}

// ReadTopicID reads the cached topic_id for a slug.
func ReadTopicID(stateBase, slug string) (int64, bool) {
	path := filepath.Join(stateBase, slug, "telegram", "topic_id")
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	s := strings.TrimSpace(string(data))
	var id int64
	if _, err := fmt.Sscanf(s, "%d", &id); err != nil {
		return 0, false
	}
	return id, true
}

// WriteTopicID caches a topic_id for a slug.
func WriteTopicID(stateBase, slug string, topicID int64) error {
	dir := EnsureStateDir(stateBase, slug)
	return os.WriteFile(filepath.Join(dir, "telegram", "topic_id"), []byte(fmt.Sprintf("%d", topicID)), 0644)
}

// ReadTopicName reads the cached topic display name for a slug.
func ReadTopicName(stateBase, slug string) string {
	path := filepath.Join(stateBase, slug, "telegram", "topic_name")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WriteTopicName caches a topic display name.
func WriteTopicName(stateBase, slug string, name string) error {
	dir := EnsureStateDir(stateBase, slug)
	return os.WriteFile(filepath.Join(dir, "telegram", "topic_name"), []byte(name), 0644)
}

// ReadBranch reads the cached current_branch for a slug.
func ReadBranch(stateBase, slug string) string {
	path := filepath.Join(stateBase, slug, "telegram", "current_branch")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WriteBranch caches the current branch for a slug.
func WriteBranch(stateBase, slug string, branch string) error {
	dir := EnsureStateDir(stateBase, slug)
	return os.WriteFile(filepath.Join(dir, "telegram", "current_branch"), []byte(branch), 0644)
}

// IsTopicClosed checks if the topic has been archived/closed.
func IsTopicClosed(stateBase, slug string) bool {
	path := filepath.Join(stateBase, slug, "telegram", "topic_closed")
	_, err := os.Stat(path)
	return err == nil
}

// MarkTopicClosed marks a topic as closed.
func MarkTopicClosed(stateBase, slug string) error {
	dir := EnsureStateDir(stateBase, slug)
	return os.WriteFile(filepath.Join(dir, "telegram", "topic_closed"), nil, 0644)
}

// MarkTopicOpen removes the closed marker.
func MarkTopicOpen(stateBase, slug string) error {
	path := filepath.Join(stateBase, slug, "telegram", "topic_closed")
	os.Remove(path)
	return nil
}

// RemoveTopicState removes all topic state files for a slug.
func RemoveTopicState(stateBase, slug string) {
	dir := filepath.Join(stateBase, slug, "telegram")
	os.Remove(filepath.Join(dir, "topic_id"))
	os.Remove(filepath.Join(dir, "topic_name"))
	os.Remove(filepath.Join(dir, "topic_closed"))
}

// ReadSummaryFile reads the session summary file for a project.
func ReadSummaryFile(stateBase, slug string) (string, bool) {
	path := filepath.Join(stateBase, slug, "session-summary.txt")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// ReadLastSummary reads the persisted last-summary.md.
func ReadLastSummary(stateBase, slug string) (string, bool) {
	path := filepath.Join(stateBase, slug, "last-summary.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// WriteLastSummary persists the summary for future use.
func WriteLastSummary(stateBase, slug, content string) error {
	dir := EnsureStateDir(stateBase, slug)
	return os.WriteFile(filepath.Join(dir, "last-summary.md"), []byte(content), 0644)
}

// ReadSummaryHash reads the last summary hash for dedup.
func ReadSummaryHash(stateBase, slug string) string {
	path := filepath.Join(stateBase, slug, "last-summary-hash")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// WriteSummaryHash stores the hash of the last sent summary.
func WriteSummaryHash(stateBase, slug, hash string) error {
	dir := EnsureStateDir(stateBase, slug)
	return os.WriteFile(filepath.Join(dir, "last-summary-hash"), []byte(hash), 0644)
}

// RemoveSummaryFile removes the session-summary.txt after consuming it.
func RemoveSummaryFile(stateBase, slug string) {
	os.Remove(filepath.Join(stateBase, slug, "session-summary.txt"))
}

// MD5Hash computes MD5 hex of a string (for dedup).
func MD5Hash(s string) string {
	h := md5.Sum([]byte(s))
	return fmt.Sprintf("%x", h)
}

// BranchToTaskName converts a git branch name to a human-readable task name.
func BranchToTaskName(branch string) string {
	prefixes := []string{"feature/", "fix/", "chore/", "docs/", "refactor/", "research/"}
	for _, p := range prefixes {
		if strings.HasPrefix(branch, p) {
			task := strings.TrimPrefix(branch, p)
			return titleCase(strings.ReplaceAll(task, "-", " "))
		}
	}
	switch branch {
	case "dev", "main", "master", "":
		return ""
	}
	return branch
}

// RepoDisplayName derives a human-readable repo name from slug.
func RepoDisplayName(slug string) string {
	return titleCase(strings.ReplaceAll(slug, "-", " "))
}

// ComposeTopicName builds the full display name for a Telegram topic.
func ComposeTopicName(repoName, taskName string) string {
	if taskName != "" {
		full := repoName + " \u2014 " + taskName // em dash
		if len(full) > 128 {
			full = full[:128]
		}
		return full
	}
	return repoName
}

func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		if len(w) > 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// ListProjectSlugs returns all slugs that have a topic_id file.
func ListProjectSlugs(stateBase string) []string {
	entries, err := os.ReadDir(stateBase)
	if err != nil {
		return nil
	}
	var slugs []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		topicFile := filepath.Join(stateBase, e.Name(), "telegram", "topic_id")
		if _, err := os.Stat(topicFile); err == nil {
			slugs = append(slugs, e.Name())
		}
	}
	return slugs
}

// FindSlugByTopicID does reverse lookup: find which slug owns a given topic ID.
func FindSlugByTopicID(stateBase string, topicID int64) string {
	for _, slug := range ListProjectSlugs(stateBase) {
		id, ok := ReadTopicID(stateBase, slug)
		if ok && id == topicID {
			return slug
		}
	}
	return ""
}
