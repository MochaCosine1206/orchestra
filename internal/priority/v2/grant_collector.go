package priority

import (
	"bufio"
	"context"
	"os"
	"regexp"
	"strings"
	"time"
)

// GrantCollector reads grant opportunity notes and creates high-priority work
// items with deadline urgency. Grant-eligible projects get the highest project
// selection priority per Steven's decision (2026-03-27).
type GrantCollector struct {
	notesDir    string // path to notes/ directory containing grant research
	defaultRepo string // default repo for grant work execution
}

// NewGrantCollector creates a collector that reads grant research files.
// defaultRepo is used as the execution target for grant work items.
func NewGrantCollector(notesDir, defaultRepo string) *GrantCollector {
	return &GrantCollector{notesDir: notesDir, defaultRepo: defaultRepo}
}

func (c *GrantCollector) Name() Source { return SourceGrant }

// grantEntry represents a parsed grant opportunity.
type grantEntry struct {
	name     string
	org      string
	deadline time.Time
	amount   string
	match    string // which Plyne project matches
}

// deadlineRe matches dates like "April 1" or "August 2026" or "Apr 1, 2026"
var deadlineRe = regexp.MustCompile(`(?i)(january|february|march|april|may|june|july|august|september|october|november|december|jan|feb|mar|apr|may|jun|jul|aug|sep|oct|nov|dec)\s+(\d{1,2})(?:\s*,?\s*(\d{4}))?`)

func (c *GrantCollector) Collect(ctx context.Context) ([]WorkItem, error) {
	// Read the grants research file
	grantsFile := c.notesDir + "/discovery-grants-signals-deep.md"
	f, err := os.Open(grantsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var items []WorkItem
	scanner := bufio.NewScanner(f)
	grantIdx := 0

	for scanner.Scan() {
		line := scanner.Text()

		// Look for grant mentions with deadlines
		if !strings.Contains(strings.ToLower(line), "grant") &&
			!strings.Contains(strings.ToLower(line), "funding") &&
			!strings.Contains(strings.ToLower(line), "sbir") &&
			!strings.Contains(strings.ToLower(line), "nlnet") &&
			!strings.Contains(strings.ToLower(line), "nsf") {
			continue
		}

		// Extract deadline if present
		dm := deadlineRe.FindStringSubmatch(line)
		var deadline time.Time
		if dm != nil {
			month := parseMonth(dm[1])
			day := 1
			if dm[2] != "" {
				d := 0
				for _, c := range dm[2] {
					d = d*10 + int(c-'0')
				}
				day = d
			}
			year := 2026
			if dm[3] != "" {
				y := 0
				for _, c := range dm[3] {
					y = y*10 + int(c-'0')
				}
				year = y
			}
			deadline = time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
		}

		// Only create items for lines with actual grant program names
		title := extractGrantTitle(line)
		if title == "" {
			continue
		}

		grantIdx++
		item := WorkItem{
			Source:       SourceGrant,
			SourceID:     "grant-" + strings.ToLower(strings.ReplaceAll(title, " ", "-")),
			Title:        "Grant: " + title,
			Description:  "Research " + title + " grant requirements, match to Plyne projects, draft application sections. " + strings.TrimSpace(line),
			Tier:         TierUser, // Grants are top priority per Steven's decision
			BasePriority: 95,       // Just below explicit user requests
			TargetRepo:   c.defaultRepo,
			Status:       StatusPending,
		}

		if !deadline.IsZero() {
			item.Deadline = &deadline
			item.DeadlineType = "hard"
		}

		items = append(items, item)
	}

	return items, nil
}

func parseMonth(s string) time.Month {
	s = strings.ToLower(s)
	months := map[string]time.Month{
		"january": time.January, "jan": time.January,
		"february": time.February, "feb": time.February,
		"march": time.March, "mar": time.March,
		"april": time.April, "apr": time.April,
		"may":  time.May,
		"june": time.June, "jun": time.June,
		"july": time.July, "jul": time.July,
		"august": time.August, "aug": time.August,
		"september": time.September, "sep": time.September,
		"october": time.October, "oct": time.October,
		"november": time.November, "nov": time.November,
		"december": time.December, "dec": time.December,
	}
	if m, ok := months[s]; ok {
		return m
	}
	return time.January
}

func extractGrantTitle(line string) string {
	// Look for known grant programs
	programs := []string{
		"NLnet", "NSF POSE", "NSF", "SBIR", "STTR", "NIH",
		"DOE", "EPA", "Gates Foundation", "EU Horizon",
		"Prototype Fund", "MacArthur", "Ford Foundation",
		"Sloan", "OpenSSF", "GitHub Fund",
	}
	for _, p := range programs {
		if strings.Contains(line, p) {
			return p
		}
	}
	return ""
}
