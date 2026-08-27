package priority

import (
	"bufio"
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

var priorityLineRe = regexp.MustCompile(`^(\d+)\.\s+(.+?)(?:\s+[—–-]{1,2}\s+(.+))?$`)

// ParsePrioritiesFile reads a priorities.md file and returns ordered user
// priorities plus the preamble goal text.
//
// Parser rules:
//   - Preamble lines starting with `>` are collected as the goal text
//   - Sections detected by `## Active Priorities` and `## Paused` / `## Never Automate` headers
//   - Numbered list items within the active section, one per line
//   - Optional repo hint after em dash (—) or double dash (--)
//   - Rank = list position
//   - Missing file returns empty results (no error — factory autopilot mode)
func ParsePrioritiesFile(path string) (goal string, priorities []UserPriority, err error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil, nil
		}
		return "", nil, err
	}
	defer f.Close()

	var preamble []string
	inActive := false
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Collect preamble lines (blockquote style)
		if strings.HasPrefix(line, ">") {
			preamble = append(preamble, strings.TrimSpace(strings.TrimPrefix(line, ">")))
			continue
		}

		// Detect section headers
		if strings.HasPrefix(line, "## ") {
			lower := strings.ToLower(line)
			if strings.Contains(lower, "active priorities") {
				inActive = true
			} else {
				inActive = false
			}
			continue
		}

		if !inActive {
			continue
		}

		// Parse numbered items
		matches := priorityLineRe.FindStringSubmatch(line)
		if matches == nil {
			continue
		}

		rank, _ := strconv.Atoi(matches[1])
		title := strings.TrimSpace(matches[2])
		repoHint := ""
		if len(matches) > 3 {
			repoHint = strings.TrimSpace(matches[3])
		}

		priorities = append(priorities, UserPriority{
			Rank:     rank,
			Title:    title,
			RepoHint: repoHint,
			Active:   true,
		})
	}

	goal = strings.Join(preamble, " ")
	return goal, priorities, scanner.Err()
}

// slugify converts a title string to a URL-friendly slug.
func slugify(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	prevDash := false
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevDash = false
		} else if !prevDash && b.Len() > 0 {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// UserCollector reads ~/.config/orchestra/priorities.md and produces
// WorkItems with Tier=TierUser.
type UserCollector struct {
	path string
}

// NewUserCollector creates a UserCollector that reads from the given path.
func NewUserCollector(path string) *UserCollector {
	return &UserCollector{path: path}
}

func (c *UserCollector) Name() Source { return SourceUser }

func (c *UserCollector) Collect(ctx context.Context) ([]WorkItem, error) {
	_, priorities, err := ParsePrioritiesFile(c.path)
	if err != nil {
		return nil, err
	}

	items := make([]WorkItem, 0, len(priorities))
	for _, p := range priorities {
		rank := p.Rank
		items = append(items, WorkItem{
			Source:           SourceUser,
			SourceID:         "user-" + slugify(p.Title),
			SourceRepo:       p.RepoHint,
			Title:            p.Title,
			Tier:             TierUser,
			BasePriority:     100,
			UserPicked:       true,
			UserPriorityRank: &rank,
		})
	}
	return items, nil
}
