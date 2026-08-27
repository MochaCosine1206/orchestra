package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TaxonomyDomain represents one of the 14 discovery domains.
type TaxonomyDomain struct {
	ID           int
	Name         string
	DeepFile     string // existing research file (e.g., "discovery-healthcare-deep.md")
	SubDomains   []string
	LastResearch time.Time
}

// TaxonomyScanner cycles through taxonomy domains, running deep research
// on each and extracting new work items. Each domain is re-researched
// on a round-robin schedule (14 domains = each visited every ~2 weeks).
type TaxonomyScanner struct {
	ProjectPath string
	StateFile   string // tracks which domain was last researched
	Logger      *slog.Logger
}

// AllDomains returns the 14 taxonomy domains with their research file mappings.
func AllDomains() []TaxonomyDomain {
	return []TaxonomyDomain{
		{1, "Healthcare & Life Sciences", "discovery-healthcare-deep.md", []string{"clinical docs", "drug discovery", "medical imaging", "patient data", "mental health", "telemedicine", "public health", "medical education", "accessibility", "genomics"}, time.Time{}},
		{2, "Education & Learning", "discovery-education-deep.md", []string{"K-12 curriculum", "higher ed", "vocational", "language learning", "special education"}, time.Time{}},
		{3, "Finance & Insurance", "discovery-finance-deep.md", []string{"personal finance", "institutional trading", "insurance", "crypto/DeFi", "regulatory compliance"}, time.Time{}},
		{4, "Science & Research", "discovery-science-deep.md", []string{"lab notebooks", "data analysis", "scientific publishing", "citizen science", "geospatial"}, time.Time{}},
		{5, "Developer Tools & Infrastructure", "discovery-devtools-deep.md", []string{"code analysis", "CI/CD", "documentation", "API tools", "observability"}, time.Time{}},
		{6, "Creative Tech & Media", "discovery-creative-deep.md", []string{"video/audio", "design", "game dev", "writing", "music"}, time.Time{}},
		{7, "Climate & Environment", "discovery-climate-deep.md", []string{"carbon tracking", "energy", "agriculture", "biodiversity", "waste"}, time.Time{}},
		{8, "Government & Civic", "discovery-government-deep.md", []string{"legislative", "FOIA", "benefits", "tax", "elections"}, time.Time{}},
		{9, "Business & Productivity", "discovery-business-deep.md", []string{"HR/ATS", "CRM", "project management", "supply chain", "legal"}, time.Time{}},
		{10, "AI Compliance & Governance", "discovery-ai-compliance-deep.md", []string{"EU AI Act", "bias auditing", "AI-BOM", "model governance"}, time.Time{}},
		{11, "Personal & Consumer", "discovery-personal-deep.md", []string{"health tracking", "productivity", "home automation", "digital nomad"}, time.Time{}},
		{12, "Local AI & On-Device", "discovery-local-ai-deep.md", []string{"local LLMs", "on-device ML", "privacy-first AI", "edge computing"}, time.Time{}},
		{13, "Grants & Funding", "discovery-grants-signals-deep.md", []string{"NSF", "NIH", "EU Horizon", "foundation grants", "SBIR"}, time.Time{}},
		{14, "Self-Improvement", "discovery-self-improvement-deep.md", []string{"Orchestra improvements", "agent quality", "research methods"}, time.Time{}},
	}
}

// ScanState tracks which domain was last researched and when.
type ScanState struct {
	LastDomainID int            `json:"last_domain_id"`
	DomainTimes  map[int]string `json:"domain_times"` // domain ID → last research ISO date
	UpdatedAt    string         `json:"updated_at"`
}

// NextDomain returns the domain that should be researched next (round-robin).
func (ts *TaxonomyScanner) NextDomain() (TaxonomyDomain, error) {
	state, err := ts.loadState()
	if err != nil {
		state = &ScanState{LastDomainID: 0, DomainTimes: make(map[int]string)}
	}

	domains := AllDomains()
	nextID := (state.LastDomainID % len(domains)) + 1 // 1-indexed round-robin

	for _, d := range domains {
		if d.ID == nextID {
			return d, nil
		}
	}
	return domains[0], nil // fallback to first
}

// RunDomainResearch executes deep research on a single taxonomy domain.
// It reads the existing research file as baseline and produces an updated version.
// Returns the path to the output file and any extracted work item suggestions.
func (ts *TaxonomyScanner) RunDomainResearch(ctx context.Context, domain TaxonomyDomain) (string, error) {
	notesDir := filepath.Join(ts.ProjectPath, "notes")
	existingFile := filepath.Join(notesDir, domain.DeepFile)

	// Read existing research as baseline
	baseline := ""
	if data, err := os.ReadFile(existingFile); err == nil {
		// Truncate to first 3000 chars to fit in prompt
		baseline = string(data)
		if len(baseline) > 3000 {
			baseline = baseline[:3000] + "\n\n[... truncated for context ...]"
		}
	}

	outputDir := filepath.Join(os.Getenv("HOME"), "symphonies", "research")
	os.MkdirAll(outputDir, 0755)
	outputFile := filepath.Join(outputDir, fmt.Sprintf("taxonomy-%d-%s-%s.md",
		domain.ID,
		strings.ReplaceAll(strings.ToLower(domain.Name), " ", "-")[:20],
		time.Now().Format("2006-01-02")))

	prompt := fmt.Sprintf(`You are a deep research specialist for Plyne Technologies' Dark Factory.

DOMAIN: %s (Domain #%d)
SUB-DOMAINS: %s
DATE: %s

EXISTING RESEARCH BASELINE:
%s

YOUR TASK:
1. Research what's NEW in this domain in the LAST 24 HOURS (since yesterday %s)
2. Search the web for: new tools launched, new open-source projects, pricing changes, regulatory updates, new gaps discovered, news articles, blog posts, announcements
3. For each finding, assess: Is this an opportunity for Plyne? Is it an ethical imperative (replacing exploitative pricing)?
4. Compare findings against existing baseline — what changed? What's genuinely new TODAY?
5. If nothing new found in last 24h, report "No new developments" with a brief market status

OUTPUT FORMAT:
## Domain Update: %s (%s)

### New Findings (since baseline)
For each new finding:
- **Title**: one-line description
- **Category**: which sub-domain
- **Opportunity**: what to build
- **Ethical**: Y/N (replacing exploitative pricing for individuals?)
- **Grant Aligned**: which grant programs match
- **Effort**: Low/Medium/High
- **Priority**: 1-5

### Updated Landscape
Brief summary of how the domain has evolved

### Regional & Multilingual Needs
- Developing country applications (Africa, Southeast Asia, Latin America)
- Non-English language requirements
- Offline-first needs for low-connectivity regions
- Regional regulatory differences (not just US/EU)
- Opportunities in underserved markets

### Suggested Work Items
Numbered list of specific items the factory should pursue, with:
- Title
- Description (2-3 sentences)
- Target users (include international/developing country users)
- Recommended tech stack
- Estimated effort
- Multilingual: Y/N (does this need non-English support?)
- Offline-first: Y/N (does this need to work without internet?)

Write output to: %s

Search the web extensively. Include international sources, not just US/EU. Only include findings with sources.`,
		domain.Name, domain.ID,
		strings.Join(domain.SubDomains, ", "),
		time.Now().Format("2006-01-02"),
		baseline,
		time.Now().Add(-24*time.Hour).Format("2006-01-02"),
		domain.Name, time.Now().Format("2006-01-02"),
		outputFile)

	ts.Logger.Info("taxonomy research starting",
		"domain", domain.Name,
		"domain_id", domain.ID,
		"output", outputFile)

	// Run claude -p from temp dir to avoid branch pollution
	tmpDir, _ := os.MkdirTemp("", fmt.Sprintf("taxonomy-scan-%d-", domain.ID))
	if tmpDir != "" {
		defer os.RemoveAll(tmpDir)
	}

	cmd := exec.CommandContext(ctx, "claude", "-p", prompt,
		"--output-format", "stream-json",
		"--verbose",
		"--model", "claude-opus-4-6",
		"--permission-mode", "bypassPermissions")
	if tmpDir != "" {
		cmd.Dir = tmpDir
	} else {
		cmd.Dir = os.TempDir()
	}
	cmd.Env = append(os.Environ(), "ORCHESTRA_DAEMON_LAUNCHED=1")

	// Redirect stdin
	devNull, _ := os.Open(os.DevNull)
	if devNull != nil {
		cmd.Stdin = devNull
		defer devNull.Close()
	}

	// Capture log
	logDir := filepath.Join(ts.ProjectPath, ".orchestra", "research")
	os.MkdirAll(logDir, 0755)
	logPath := filepath.Join(logDir, fmt.Sprintf("taxonomy-%d-%s.log", domain.ID, time.Now().Format("20060102")))
	if logFile, err := os.Create(logPath); err == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		defer logFile.Close()
	}

	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("starting taxonomy research: %w", err)
	}

	ts.Logger.Info("taxonomy research running", "pid", cmd.Process.Pid)

	if err := cmd.Wait(); err != nil {
		return "", fmt.Errorf("taxonomy research failed: %w", err)
	}

	// Update state
	ts.recordDomainResearch(domain.ID)

	ts.Logger.Info("taxonomy research complete",
		"domain", domain.Name,
		"output", outputFile)

	return outputFile, nil
}

func (ts *TaxonomyScanner) loadState() (*ScanState, error) {
	data, err := os.ReadFile(ts.StateFile)
	if err != nil {
		return nil, err
	}
	var state ScanState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

func (ts *TaxonomyScanner) recordDomainResearch(domainID int) {
	state, err := ts.loadState()
	if err != nil {
		state = &ScanState{DomainTimes: make(map[int]string)}
	}
	state.LastDomainID = domainID
	state.DomainTimes[domainID] = time.Now().Format("2006-01-02")
	state.UpdatedAt = time.Now().Format(time.RFC3339)

	data, _ := json.MarshalIndent(state, "", "  ")
	os.WriteFile(ts.StateFile, data, 0644)
}
