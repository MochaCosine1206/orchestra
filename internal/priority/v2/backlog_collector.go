package priority

import (
	"bufio"
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// Matches backlog item headers: ### B-001: Title, ### I-039: Title, ### G-012: Title
	backlogItemRe = regexp.MustCompile(`^###\s+([BIG]-\d+):\s+(.+)`)

	// Matches priority tier section headers: ## P0: Blockers, ## P1: Core, etc.
	tierSectionRe = regexp.MustCompile(`^##\s+P(\d):`)

	// Matches status lines: **Status:** DONE, **Status:** Open, etc.
	statusLineRe = regexp.MustCompile(`^\*\*Status:\*\*\s+(.+)`)

	// Matches category lines: **Category:** Infrastructure, etc.
	categoryLineRe = regexp.MustCompile(`^\*\*Category:\*\*\s+(.+)`)
)

// backlogPriorityMap maps P-level to BasePriority score.
var backlogPriorityMap = map[int]int{
	0: 100,
	1: 80,
	2: 60,
	3: 40,
	4: 20,
	5: 10,
}

// backlogTierMap maps P-level to work item Tier.
var backlogTierMap = map[int]Tier{
	0: TierGoalImpl,
	1: TierGoalResearch,
	2: TierGoalResearch,
	3: TierTechDebt,
	4: TierTechDebt,
	5: TierExploratory,
}

type backlogEntry struct {
	id       string
	title    string
	pLevel   int
	status   string
	category string
}

// BacklogCollector reads BACKLOG.md files from registered repos and produces
// WorkItems for non-DONE backlog entries.
type BacklogCollector struct {
	repos []string
}

// NewBacklogCollector creates a BacklogCollector that reads BACKLOG.md from each repo path.
func NewBacklogCollector(repos []string) *BacklogCollector {
	return &BacklogCollector{repos: repos}
}

func (c *BacklogCollector) Name() Source { return SourceBacklog }

func (c *BacklogCollector) Collect(ctx context.Context) ([]WorkItem, error) {
	var items []WorkItem
	for _, repo := range c.repos {
		entries, err := parseBacklogFile(filepath.Join(repo, "BACKLOG.md"))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, e := range entries {
			bp, ok := backlogPriorityMap[e.pLevel]
			if !ok {
				bp = 10
			}
			tier, ok := backlogTierMap[e.pLevel]
			if !ok {
				tier = TierExploratory
			}
			items = append(items, WorkItem{
				Source:       SourceBacklog,
				SourceID:     e.id,
				SourceRepo:   repo,
				Title:        e.title,
				Description:  e.category,
				Tier:         tier,
				BasePriority: bp,
			})
		}
	}
	return items, nil
}

// parseBacklogFile reads a BACKLOG.md and returns non-DONE entries with their P-level.
func parseBacklogFile(path string) ([]backlogEntry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []backlogEntry
	var current *backlogEntry
	currentPLevel := -1

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Detect tier section: ## P0: ...
		if m := tierSectionRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				maybeAppend(&entries, current)
			}
			p := int(m[1][0] - '0')
			currentPLevel = p
			current = nil
			continue
		}

		// Detect item header: ### B-001: Title
		if m := backlogItemRe.FindStringSubmatch(line); m != nil {
			if current != nil {
				maybeAppend(&entries, current)
			}
			current = &backlogEntry{
				id:     m[1],
				title:  strings.TrimSpace(m[2]),
				pLevel: currentPLevel,
			}
			continue
		}

		if current == nil {
			continue
		}

		// Detect status
		if m := statusLineRe.FindStringSubmatch(line); m != nil {
			current.status = strings.TrimSpace(m[1])
			continue
		}

		// Detect category
		if m := categoryLineRe.FindStringSubmatch(line); m != nil {
			current.category = strings.TrimSpace(m[1])
			continue
		}
	}

	// Flush last entry
	if current != nil {
		maybeAppend(&entries, current)
	}

	return entries, scanner.Err()
}

// maybeAppend adds the entry if it's not done.
func maybeAppend(entries *[]backlogEntry, e *backlogEntry) {
	if isDone(e.status) {
		return
	}
	*entries = append(*entries, *e)
}

// isDone returns true if the status indicates the item is completed.
func isDone(status string) bool {
	s := strings.ToLower(status)
	return strings.Contains(s, "done")
}
