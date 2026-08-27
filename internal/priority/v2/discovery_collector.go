package priority

import (
	"bufio"
	"context"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// DiscoveryCollector reads the compiled discovery report and extracts ranked
// ideas from the "Top 30 Ideas" table into WorkItems.
type DiscoveryCollector struct {
	reportPath string
}

// NewDiscoveryCollector creates a collector that reads a discovery report markdown file.
func NewDiscoveryCollector(reportPath string) *DiscoveryCollector {
	return &DiscoveryCollector{reportPath: reportPath}
}

func (c *DiscoveryCollector) Name() Source { return SourceDiscovery }

// discoveryIdea represents a parsed row from the Top 30 table.
type discoveryIdea struct {
	rank        int
	title       string
	domain      string
	score       float64
	effort      string
	feasibility float64
	impact      float64
	uniqueness  float64
}

// tableRowRe matches markdown table rows: | 1 | Title | Domain | 5 | 5 | 5 | 5 | 500 | M |
var tableRowRe = regexp.MustCompile(`^\|\s*(\d+)\s*\|([^|]+)\|([^|]+)\|([^|]+)\|([^|]+)\|([^|]+)\|([^|]+)\|([^|]+)\|([^|]+)\|`)

func (c *DiscoveryCollector) Collect(ctx context.Context) ([]WorkItem, error) {
	f, err := os.Open(c.reportPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // no report yet — silent
		}
		return nil, err
	}
	defer f.Close()

	var items []WorkItem
	inTop30 := false

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		// Detect the Top 30 section
		if strings.Contains(line, "Top 30") || strings.Contains(line, "Top 15") || strings.Contains(line, "Master Ranked") {
			inTop30 = true
			continue
		}

		// Stop at next section header
		if inTop30 && strings.HasPrefix(line, "## ") && !strings.Contains(line, "Top") {
			break
		}

		if !inTop30 {
			continue
		}

		// Try to parse as table row
		m := tableRowRe.FindStringSubmatch(line)
		if m == nil {
			// Also try simpler format: | rank | title | domain | score | effort |
			idea := parseSimpleRow(line)
			if idea != nil {
				items = append(items, ideaToWorkItem(*idea))
			}
			continue
		}

		rank, _ := strconv.Atoi(strings.TrimSpace(m[1]))
		title := strings.TrimSpace(m[2])
		// Remove markdown bold
		title = strings.ReplaceAll(title, "**", "")
		domain := strings.TrimSpace(m[3])
		f_score, _ := strconv.ParseFloat(strings.TrimSpace(m[4]), 64)
		i_score, _ := strconv.ParseFloat(strings.TrimSpace(m[5]), 64)
		u_score, _ := strconv.ParseFloat(strings.TrimSpace(m[6]), 64)
		c_score, _ := strconv.ParseFloat(strings.TrimSpace(m[7]), 64)
		composite, _ := strconv.ParseFloat(strings.TrimSpace(m[8]), 64)
		effort := strings.TrimSpace(m[9])

		idea := discoveryIdea{
			rank:        rank,
			title:       title,
			domain:      domain,
			score:       composite,
			effort:      effort,
			feasibility: f_score / 5.0, // normalize 1-5 → 0-1
			impact:      i_score / 5.0,
			uniqueness:  u_score / 5.0,
		}
		_ = c_score // cost score available but not separately stored

		items = append(items, ideaToWorkItem(idea))
	}

	return items, nil
}

// parseSimpleRow handles | rank | title | domain | score | effort | format
func parseSimpleRow(line string) *discoveryIdea {
	parts := strings.Split(line, "|")
	if len(parts) < 6 {
		return nil
	}
	rank, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return nil
	}
	title := strings.TrimSpace(parts[2])
	title = strings.ReplaceAll(title, "**", "")
	if title == "" || title == "---" || title == "#" {
		return nil
	}
	domain := strings.TrimSpace(parts[3])
	score, _ := strconv.ParseFloat(strings.TrimSpace(parts[4]), 64)
	effort := ""
	if len(parts) > 5 {
		effort = strings.TrimSpace(parts[5])
	}

	return &discoveryIdea{
		rank:   rank,
		title:  title,
		domain: domain,
		score:  score,
		effort: effort,
	}
}

func ideaToWorkItem(idea discoveryIdea) WorkItem {
	// Determine tier based on score
	tier := TierGoalResearch // default: discovered ideas start as research
	if idea.score >= 400 {
		tier = TierGoalImpl // high-scoring ideas ready for implementation
	}

	// Estimate effort hours from label
	var effortHours float64
	switch strings.ToUpper(strings.TrimSpace(idea.effort)) {
	case "S":
		effortHours = 20
	case "M":
		effortHours = 80
	case "L":
		effortHours = 200
	default:
		effortHours = 80
	}

	f := idea.feasibility
	imp := idea.impact
	u := idea.uniqueness
	return WorkItem{
		Source:       SourceDiscovery,
		SourceID:     "disc-" + strconv.Itoa(idea.rank),
		Title:        idea.title,
		Description:  "Domain: " + idea.domain + ". Discovery rank #" + strconv.Itoa(idea.rank) + ". Composite score: " + strconv.FormatFloat(idea.score, 'f', 0, 64),
		Tier:         tier,
		BasePriority: int(idea.score / 5),
		Feasibility:  &f,
		Impact:       &imp,
		Uniqueness:   &u,
		EffortHours:  &effortHours,
		Status:       StatusPending,
	}
}
