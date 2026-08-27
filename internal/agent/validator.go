package agent

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/MochaCosine1206/orchestra/internal/db"
	"github.com/MochaCosine1206/orchestra/internal/healing"
)

// SkipReport records a file that an agent intentionally skipped (read but didn't modify).
type SkipReport struct {
	FilePath string `json:"file_path"`
	Reason   string `json:"reason"`
}

// ValidationResult captures whether a task's output is valid.
type ValidationResult struct {
	OK            bool
	Reason        string       // e.g. "implementer_no_changes", "researcher_no_commits"
	ResultSummary string       // extracted for blackboard predecessor injection (G39)
	SkipReports   []SkipReport // files the agent read but didn't modify (B-207)
}

// ValidateTaskOutput dispatches to role-specific validators.
func ValidateTaskOutput(ctx context.Context, d *db.DB, taskID, repoRoot, logsDir string) (*ValidationResult, error) {
	task, err := d.GetTaskByID(ctx, taskID)
	if err != nil || task == nil {
		return nil, fmt.Errorf("task %s not found", taskID)
	}

	agent, err := d.GetAgentByTask(ctx, taskID)
	if err != nil || agent == nil {
		return nil, fmt.Errorf("no agent for task %s", taskID)
	}

	role := Role(agent.Role)
	worktree := ""
	if task.Worktree.Valid {
		worktree = task.Worktree.String
	}
	// Detect base branch: use the branch the worktree was created from.
	// Worktrees branch off the current HEAD of the parent repo, so we find
	// the merge-base between HEAD and common branch names.
	baseBranch := detectBaseBranch(worktree)

	switch role {
	case RoleImplementer:
		return validateImplementer(ctx, d, worktree, baseBranch, logsDir, taskID)
	case RoleResearcher, RoleArchitect:
		taskDesc := ""
		if task.Description.Valid {
			taskDesc = task.Description.String
		}
		return validateResearcherOrArchitect(worktree, baseBranch, logsDir, taskID, role, taskDesc)
	case RoleScout:
		return validateScout(logsDir, taskID)
	case RoleReviewer:
		return validateReviewer(worktree, baseBranch, logsDir, taskID)
	case RoleEditor:
		return validateEditor(worktree, baseBranch, logsDir, taskID)
	case RoleIllustrator:
		return validateIllustrator(worktree, baseBranch, logsDir, taskID)
	default:
		return &ValidationResult{OK: true, Reason: "unknown_role_skip"}, nil
	}
}

// validateImplementer checks that the implementer actually changed files
// and that all owned files were modified (completeness check).
func validateImplementer(ctx context.Context, d *db.DB, worktree, baseBranch, logsDir, taskID string) (*ValidationResult, error) {
	if worktree == "" {
		return &ValidationResult{OK: false, Reason: "implementer_no_worktree"}, nil
	}

	// Check for real changes (not just .claude/ or .orchestra/ files)
	diffStat, err := GitCmd(worktree, "diff", "--stat", baseBranch+"..HEAD")
	if err != nil {
		// If branch doesn't exist yet, check unstaged
		diffStat, _ = GitCmd(worktree, "diff", "--stat")
	}

	// Filter out .claude/, .orchestra/, and .mcp.json
	realChanges := filterChanges(diffStat)
	if realChanges == "" {
		// Check staged changes
		stagedStat, _ := GitCmd(worktree, "diff", "--stat", "--cached")
		realChanges = filterChanges(stagedStat)
	}
	if realChanges == "" {
		// Check untracked files
		untracked, _ := GitCmd(worktree, "ls-files", "--others", "--exclude-standard")
		untracked = filterChanges(untracked)
		if untracked == "" {
			return &ValidationResult{OK: false, Reason: "implementer_no_changes"}, nil
		}
	}

	// Check file coverage completeness: all owned files must be modified
	locks, _ := d.ListFileLocksForTask(ctx, taskID)
	if len(locks) > 0 {
		modifiedFiles, _ := GitCmd(worktree, "diff", "--name-only", baseBranch+"..HEAD")
		modifiedSet := make(map[string]bool)
		for _, f := range strings.Split(modifiedFiles, "\n") {
			f = strings.TrimSpace(f)
			if f != "" {
				modifiedSet[f] = true
			}
		}

		var unmodified []string
		for _, lock := range locks {
			if !modifiedSet[lock.FilePath] {
				unmodified = append(unmodified, lock.FilePath)
			}
		}

		if len(unmodified) > 0 {
			skipReports := extractSkipReasons(logsDir, taskID, unmodified)
			return &ValidationResult{
				OK:            false,
				Reason:        "implementer_partial_work",
				ResultSummary: fmt.Sprintf("%d/%d owned files unmodified: %s", len(unmodified), len(locks), strings.Join(unmodified, ", ")),
				SkipReports:   skipReports,
			}, nil
		}
	}

	// G156/G160: Skip build validation if the task didn't modify any .go files.
	// CSS-only, asset-only, and template-only tasks should not fail because
	// go build doesn't pass — they don't produce Go code. The phase gate
	// (post-merge) will catch any Go compilation issues.
	hasGoFiles := false
	if modifiedFiles, err := GitCmd(worktree, "diff", "--name-only", baseBranch+"..HEAD"); err == nil {
		for _, f := range strings.Split(modifiedFiles, "\n") {
			if strings.HasSuffix(strings.TrimSpace(f), ".go") {
				hasGoFiles = true
				break
			}
		}
	}

	// G112: Orchestrator-side build+test verification.
	// Agents can declare "done" without actually running tests. This catches
	// non-compiling code and failing tests before the branch reaches merge.
	// G115: When conductor:test_cmd is set in the blackboard, use it instead of
	// the default go build/test (avoids building the full repo in experiment clones).
	testCmdOverride, _ := d.GetBlackboardValue(ctx, "conductor:test_cmd")
	var buildTestOK bool
	var buildTestOutput string
	if !hasGoFiles {
		// No .go files modified — skip build validation (G156/G160)
		buildTestOK = true
	} else {
		buildTestOK, buildTestOutput = runBuildAndTest(worktree, testCmdOverride)
	}
	if !buildTestOK {
		// Attempt healing: parse the build error, try one fix, re-run build.
		sessionID, _ := d.GetBlackboardValue(ctx, "conductor:session_id")
		if sessionID != "" {
			buildErrors := healing.ParseBuildError(buildTestOutput)
			if len(buildErrors) > 0 {
				healer := healing.NewHealer(sessionID, d)
				defer healer.Close()
				result := healer.Heal(ctx, taskID, buildTestOutput, worktree, nil, nil)
				if result.Fixed {
					// Re-run build after fix
					retryOK, retryOutput := runBuildAndTest(worktree, testCmdOverride)
					if retryOK {
						healer.Confirm(result.FixID)
						buildTestOK = true
						buildTestOutput = ""
					} else {
						buildTestOutput = retryOutput
					}
				}
			}
		}
	}
	if !buildTestOK {
		return &ValidationResult{
			OK:            false,
			Reason:        "implementer_build_or_test_failed",
			ResultSummary: truncate(buildTestOutput, 2000),
		}, nil
	}

	// P2: JSONL trace audit — scan agent log for go test/make test evidence (observability only)
	auditTestEvidence(logsDir, taskID)

	// Build result summary
	summary, _ := GitCmd(worktree, "diff", baseBranch, "--stat")
	if summary == "" {
		summary, _ = GitCmd(worktree, "diff", "--stat")
	}
	return &ValidationResult{
		OK:            true,
		ResultSummary: truncate(summary, 2000),
	}, nil
}

// validateResearcherOrArchitect checks for commits + markdown quality.
func validateResearcherOrArchitect(worktree, baseBranch, logsDir, taskID string, role Role, taskDesc string) (*ValidationResult, error) {
	if worktree == "" {
		return &ValidationResult{OK: false, Reason: string(role) + "_no_worktree"}, nil
	}

	// Must have commits ahead of base
	countStr, err := GitCmd(worktree, "rev-list", baseBranch+"..HEAD", "--count")
	if err != nil || strings.TrimSpace(countStr) == "0" {
		return &ValidationResult{OK: false, Reason: string(role) + "_no_commits"}, nil
	}

	// Find markdown files
	mdFiles, _ := findMarkdownFiles(worktree, baseBranch)
	if len(mdFiles) == 0 {
		return &ValidationResult{OK: false, Reason: string(role) + "_no_markdown"}, nil
	}

	// Check markdown quality on first file
	for _, mdFile := range mdFiles {
		content, err := os.ReadFile(mdFile)
		if err != nil {
			continue
		}
		text := string(content)
		if len(text) < 500 {
			return &ValidationResult{OK: false, Reason: string(role) + "_markdown_too_short"}, nil
		}
		headings := strings.Count(text, "\n## ") + strings.Count(text, "\n### ")
		if headings < 2 {
			return &ValidationResult{OK: false, Reason: string(role) + "_insufficient_headings"}, nil
		}
		// Only require URLs for external research tasks; internal audits/refactors
		// produce valid output without external references (G94 false positive fix).
		requireURLs := true
		descLower := strings.ToLower(taskDesc)
		for _, kw := range []string{"internal", "codebase", "audit", "refactor", "architecture", "design doc", "decision", "style guide", "outline", "specification", "interlude", "chapter plan"} {
			if strings.Contains(descLower, kw) {
				requireURLs = false
				break
			}
		}
		if strings.Contains(text, "```") {
			requireURLs = false
		}
		if requireURLs {
			urls := strings.Count(text, "http://") + strings.Count(text, "https://")
			if urls < 3 {
				return &ValidationResult{OK: false, Reason: string(role) + "_insufficient_urls"}, nil
			}
		}
		break // only check first file
	}

	// Score research output for observability (researcher role only)
	if role == RoleResearcher && len(mdFiles) > 0 {
		if content, err := os.ReadFile(mdFiles[0]); err == nil {
			score := ScoreResearchOutput(string(content))
			slog.Info("research quality score",
				"task", taskID, "role", string(role),
				"methodology", score.Methodology, "sources", score.Sources,
				"novelty", score.Novelty, "actionability", score.Actionability,
				"clarity", score.Clarity, "completeness", score.Completeness,
				"overall", score.Overall, "pass", score.Pass)
		}
	}

	// Build result summary: git log + first 2000 chars of markdown
	logOutput, _ := GitCmd(worktree, "log", baseBranch+"..HEAD", "--oneline")
	summary := logOutput
	if len(mdFiles) > 0 {
		if content, err := os.ReadFile(mdFiles[0]); err == nil {
			summary += "\n---\n" + string(content)
		}
	}

	return &ValidationResult{
		OK:            true,
		ResultSummary: truncate(summary, 2000),
	}, nil
}

// validateScout checks that the result JSON in the log is substantial.
func validateScout(logsDir, taskID string) (*ValidationResult, error) {
	result := extractResultFromLog(logsDir, taskID)
	if len(result) < 100 {
		return &ValidationResult{OK: false, Reason: "scout_result_too_short"}, nil
	}

	return &ValidationResult{
		OK:            true,
		ResultSummary: truncate(result, 2000),
	}, nil
}

// validateReviewer checks for commits OR substantial result.
func validateReviewer(worktree, baseBranch, logsDir, taskID string) (*ValidationResult, error) {
	hasCommits := false
	if worktree != "" {
		countStr, err := GitCmd(worktree, "rev-list", baseBranch+"..HEAD", "--count")
		if err == nil && strings.TrimSpace(countStr) != "0" {
			hasCommits = true
		}
	}

	result := extractResultFromLog(logsDir, taskID)
	if !hasCommits && len(result) < 100 {
		return &ValidationResult{OK: false, Reason: "reviewer_no_output"}, nil
	}

	summary := result
	if hasCommits && worktree != "" {
		logOutput, _ := GitCmd(worktree, "log", baseBranch+"..HEAD", "--oneline")
		summary = logOutput
	}

	return &ValidationResult{
		OK:            true,
		ResultSummary: truncate(summary, 2000),
	}, nil
}

// ExtractResultSummary generates a role-appropriate summary for predecessor injection.
func ExtractResultSummary(role Role, worktree, baseBranch, logsDir, taskID string) string {
	switch role {
	case RoleImplementer:
		if worktree != "" {
			summary, _ := GitCmd(worktree, "diff", baseBranch, "--stat")
			return truncate(summary, 2000)
		}
	case RoleResearcher, RoleArchitect:
		if worktree != "" {
			logOutput, _ := GitCmd(worktree, "log", baseBranch+"..HEAD", "--oneline")
			mdFiles, _ := findMarkdownFiles(worktree, baseBranch)
			summary := logOutput
			if len(mdFiles) > 0 {
				if content, err := os.ReadFile(mdFiles[0]); err == nil {
					summary += "\n---\n" + string(content)
				}
			}
			return truncate(summary, 2000)
		}
	case RoleScout, RoleReviewer:
		return truncate(extractResultFromLog(logsDir, taskID), 2000)
	}
	return ""
}

// DetectBaseBranch finds the base branch of a git repo.
// Tries common branch names and picks the one with a valid merge-base to HEAD.
// Falls back to "dev" if nothing is found.
func DetectBaseBranch(repoPath string) string {
	return detectBaseBranch(repoPath)
}

// detectBaseBranch finds the branch the worktree was created from.
// Tries common branch names and picks the one with a valid merge-base to HEAD.
// Falls back to "dev" if nothing is found.
func detectBaseBranch(worktree string) string {
	if worktree == "" {
		return "dev"
	}
	// Try common base branch names in priority order
	candidates := []string{"dev", "development", "main", "master"}
	for _, branch := range candidates {
		// Check if the branch exists locally
		if _, err := GitCmd(worktree, "rev-parse", "--verify", branch); err == nil {
			// Verify there's a merge-base (i.e., the branch is an ancestor)
			if _, err := GitCmd(worktree, "merge-base", branch, "HEAD"); err == nil {
				return branch
			}
		}
		// Fallback: check remote ref (detached-HEAD clones only have remotes/origin/*)
		remote := "origin/" + branch
		if _, err := GitCmd(worktree, "rev-parse", "--verify", remote); err == nil {
			// Create a local tracking branch from the remote ref
			GitCmd(worktree, "branch", branch, remote)
			if _, err := GitCmd(worktree, "merge-base", branch, "HEAD"); err == nil {
				return branch
			}
		}
	}
	return "dev"
}

// QualityScore captures multi-dimensional quality metrics for research output.
type QualityScore struct {
	Methodology   int     // 1-5: systematic structure (methodology/approach sections)
	Sources       int     // 1-5: URL/reference count
	Novelty       int     // 1-5: word depth per section
	Actionability int     // 1-5: actionable recommendation patterns
	Clarity       int     // 1-5: headings, tables, structural elements
	Completeness  int     // 1-5: section depth, limitation/gap coverage
	Overall       float64 // average of all dimensions
	Pass          bool    // true if all dimensions >= 3
}

var urlRe = regexp.MustCompile(`https?://[^\s)>\]"']+`)

// ScoreResearchOutput evaluates research content using heuristic markdown parsing.
func ScoreResearchOutput(content string) QualityScore {
	if content == "" {
		return QualityScore{
			Methodology:   1,
			Sources:       1,
			Novelty:       1,
			Actionability: 1,
			Clarity:       1,
			Completeness:  1,
			Overall:       1.0,
			Pass:          false,
		}
	}

	lines := strings.Split(content, "\n")

	// Sources: count unique URLs
	urls := urlRe.FindAllString(content, -1)
	sourcesScore := clampScore(len(urls), []int{0, 2, 5, 10, 15})

	// Clarity: count headings + check for tables
	headingCount := 0
	hasTables := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			headingCount++
		}
		if strings.Contains(trimmed, "|") && strings.Count(trimmed, "|") >= 2 {
			hasTables = true
		}
	}
	clarityScore := clampScore(headingCount, []int{0, 2, 4, 7, 10})
	if hasTables && clarityScore < 5 {
		clarityScore++
	}
	if clarityScore > 5 {
		clarityScore = 5
	}

	// Completeness: word count per section + limitation/gap/caveat sections
	sections := splitSections(content)
	shortSections := 0
	totalWords := 0
	for _, sec := range sections {
		wc := wordCount(sec)
		totalWords += wc
		if wc < 50 {
			shortSections++
		}
	}
	hasLimitations := false
	lowerContent := strings.ToLower(content)
	for _, kw := range []string{"limitation", "gap", "caveat", "constraint", "trade-off", "tradeoff"} {
		if strings.Contains(lowerContent, kw) {
			hasLimitations = true
			break
		}
	}
	completenessScore := 1
	if len(sections) > 0 {
		goodRatio := float64(len(sections)-shortSections) / float64(len(sections))
		if goodRatio >= 0.8 {
			completenessScore = 4
		} else if goodRatio >= 0.6 {
			completenessScore = 3
		} else if goodRatio >= 0.3 {
			completenessScore = 2
		}
	}
	if hasLimitations && completenessScore < 5 {
		completenessScore++
	}
	if totalWords >= 1000 && completenessScore < 5 {
		completenessScore++
	}
	if completenessScore > 5 {
		completenessScore = 5
	}

	// Novelty: word depth per section (deeper sections = more novel content)
	noveltyScore := 1
	if len(sections) > 0 {
		avgWords := totalWords / len(sections)
		if avgWords >= 200 {
			noveltyScore = 5
		} else if avgWords >= 150 {
			noveltyScore = 4
		} else if avgWords >= 100 {
			noveltyScore = 3
		} else if avgWords >= 50 {
			noveltyScore = 2
		}
	}

	// Actionability: count recommendation patterns
	actionPatterns := regexp.MustCompile(`(?i)(recommend|should|suggest|action item|next step|todo|to-do|consider|implement|adopt|migrate|use .+ instead)`)
	actionMatches := actionPatterns.FindAllString(content, -1)
	actionabilityScore := clampScore(len(actionMatches), []int{0, 1, 3, 6, 10})

	// Methodology: systematic structure (methodology/approach/method sections)
	methodologyScore := 1
	methodKeywords := []string{"methodology", "approach", "method", "framework", "process", "procedure", "analysis", "evaluation"}
	methodCount := 0
	for _, kw := range methodKeywords {
		if strings.Contains(lowerContent, kw) {
			methodCount++
		}
	}
	if methodCount >= 4 {
		methodologyScore = 5
	} else if methodCount >= 3 {
		methodologyScore = 4
	} else if methodCount >= 2 {
		methodologyScore = 3
	} else if methodCount >= 1 {
		methodologyScore = 2
	}

	dims := []int{methodologyScore, sourcesScore, noveltyScore, actionabilityScore, clarityScore, completenessScore}
	sum := 0
	pass := true
	for _, d := range dims {
		sum += d
		if d < 3 {
			pass = false
		}
	}
	overall := float64(sum) / float64(len(dims))

	return QualityScore{
		Methodology:   methodologyScore,
		Sources:       sourcesScore,
		Novelty:       noveltyScore,
		Actionability: actionabilityScore,
		Clarity:       clarityScore,
		Completeness:  completenessScore,
		Overall:       overall,
		Pass:          pass,
	}
}

// clampScore maps a count to a 1-5 score using thresholds.
// thresholds[i] is the minimum count for score i+1.
func clampScore(count int, thresholds []int) int {
	score := 1
	for i := len(thresholds) - 1; i >= 0; i-- {
		if count >= thresholds[i] {
			score = i + 1
			break
		}
	}
	if score > 5 {
		score = 5
	}
	return score
}

// splitSections splits markdown content by heading boundaries.
func splitSections(content string) []string {
	lines := strings.Split(content, "\n")
	var sections []string
	var current []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if len(current) > 0 {
				sections = append(sections, strings.Join(current, "\n"))
			}
			current = nil
		} else {
			current = append(current, line)
		}
	}
	if len(current) > 0 {
		sections = append(sections, strings.Join(current, "\n"))
	}
	return sections
}

// wordCount returns the number of whitespace-delimited words.
func wordCount(s string) int {
	return len(strings.Fields(s))
}

// runBuildAndTest runs build and test verification in the given worktree.
// When testCmdOverride is non-empty, it runs that command instead of the
// default `go build ./...` + `go test ./...`. This allows the conductor to
// specify a scoped test command (e.g. `go build ./internal/alpha/...`) that
// avoids building the entire repo in experiment worktrees (G115).
// Returns (true, "") on success, or (false, combinedOutput) on failure.
func runBuildAndTest(worktree, testCmdOverride string) (bool, string) {
	if worktree == "" {
		return true, "" // no worktree to verify
	}

	// When an override command is provided, run it directly instead of
	// the default go build + go test sequence.
	if testCmdOverride != "" {
		cmd := exec.Command("sh", "-c", testCmdOverride)
		cmd.Dir = worktree
		out, err := cmd.CombinedOutput()
		if err != nil {
			return false, fmt.Sprintf("test_cmd failed:\n%s", string(out))
		}
		return true, ""
	}

	// Check if this is a Go project (has go.mod)
	if _, err := os.Stat(filepath.Join(worktree, "go.mod")); err != nil {
		// Not a Go project — skip verification (generalization deferred)
		return true, ""
	}

	// Step 1: go build
	buildCmd := exec.Command("go", "build", "./...")
	buildCmd.Dir = worktree
	buildOut, buildErr := buildCmd.CombinedOutput()
	if buildErr != nil {
		return false, fmt.Sprintf("go build failed:\n%s", string(buildOut))
	}

	// Step 2: go test — scope to modified packages only (G164).
	// Running `go test ./...` includes stress tests and unrelated packages
	// that can timeout or fail for reasons unrelated to the agent's work.
	// Instead, detect which Go packages the agent modified and test only those.
	testPkgs := modifiedGoPackages(worktree)
	if len(testPkgs) == 0 {
		// Fallback: if we can't determine packages, test the whole repo
		testPkgs = []string{"./..."}
	}
	testArgs := append([]string{"test"}, testPkgs...)
	testArgs = append(testArgs, "-count=1", "-short", "-timeout=120s")
	testCmd := exec.Command("go", testArgs...)
	testCmd.Dir = worktree
	testOut, testErr := testCmd.CombinedOutput()
	if testErr != nil {
		return false, fmt.Sprintf("go test failed:\n%s", string(testOut))
	}

	return true, ""
}

// modifiedGoPackages returns the list of Go packages that have modified .go
// files compared to the base branch. Returns package paths relative to the
// module root (e.g., "./internal/dashboard/...").
func modifiedGoPackages(worktree string) []string {
	base := detectBaseBranch(worktree)
	diffOut, err := GitCmd(worktree, "diff", "--name-only", base+"..HEAD")
	if err != nil {
		return nil
	}

	pkgSet := make(map[string]bool)
	for _, f := range strings.Split(diffOut, "\n") {
		f = strings.TrimSpace(f)
		if strings.HasSuffix(f, ".go") {
			dir := filepath.Dir(f)
			// Convert to Go package path: ./dir/...
			pkg := "./" + dir
			pkgSet[pkg] = true
		}
	}

	if len(pkgSet) == 0 {
		return nil
	}

	pkgs := make([]string, 0, len(pkgSet))
	for pkg := range pkgSet {
		pkgs = append(pkgs, pkg)
	}
	return pkgs
}

// auditTestEvidence scans the agent's JSONL log for evidence of `go test` or
// `make test` invocations. Logs a warning if no evidence is found. This is
// observability only — it does not block validation (G112 P2).
func auditTestEvidence(logsDir, taskID string) {
	logFile := filepath.Join(logsDir, taskID+".jsonl")
	data, err := os.ReadFile(logFile)
	if err != nil {
		return // no log file — skip audit
	}

	content := string(data)
	testPatterns := []string{"go test", "make test", "npm test", "pytest", "cargo test"}
	for _, p := range testPatterns {
		if strings.Contains(content, p) {
			return // found evidence
		}
	}
	slog.Warn("no test execution evidence in agent JSONL log", "task", taskID)
}

// --- Helpers ---

// GitCmd runs a git command in the given directory and returns trimmed output.
func GitCmd(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func filterChanges(stat string) string {
	var lines []string
	for _, line := range strings.Split(stat, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip .claude/, .orchestra/, and .mcp.json entries
		if strings.Contains(line, ".claude/") || strings.Contains(line, ".orchestra/") || strings.Contains(line, ".mcp.json") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func findMarkdownFiles(worktree, baseBranch string) ([]string, error) {
	// Get list of changed files
	diffOutput, err := GitCmd(worktree, "diff", "--name-only", baseBranch+"..HEAD")
	if err != nil {
		return nil, err
	}

	var mdFiles []string
	for _, f := range strings.Split(diffOutput, "\n") {
		f = strings.TrimSpace(f)
		if strings.HasSuffix(f, ".md") {
			fullPath := filepath.Join(worktree, f)
			if _, err := os.Stat(fullPath); err == nil {
				mdFiles = append(mdFiles, fullPath)
			}
		}
	}
	return mdFiles, nil
}

func extractResultFromLog(logsDir, taskID string) string {
	logFile := filepath.Join(logsDir, taskID+".jsonl")
	data, err := os.ReadFile(logFile)
	if err != nil {
		return ""
	}

	// Search for result line from the end
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.Contains(line, `"type":"result"`) || strings.Contains(line, `"type": "result"`) {
			return line
		}
	}
	return ""
}

func truncate(s string, maxLen int) string {
	if len(s) > maxLen {
		return s[:maxLen]
	}
	return s
}

// extractSkipReasons scans the JSONL log to determine if the agent read
// unmodified files (indicating intentional skipping rather than omission).
// For each unmodified file, it checks if a Read tool call was made on it
// and infers a "read but not modified" skip reason.
func extractSkipReasons(logsDir, taskID string, unmodifiedFiles []string) []SkipReport {
	logFile := filepath.Join(logsDir, taskID+".jsonl")
	data, err := os.ReadFile(logFile)
	if err != nil {
		return nil
	}

	content := string(data)
	var reports []SkipReport

	for _, f := range unmodifiedFiles {
		// Check if the file was read (agent saw it but chose not to modify)
		baseName := filepath.Base(f)
		if strings.Contains(content, f) || strings.Contains(content, baseName) {
			reports = append(reports, SkipReport{
				FilePath: f,
				Reason:   "file was read but not modified — agent may have found no concrete insertion point",
			})
		} else {
			reports = append(reports, SkipReport{
				FilePath: f,
				Reason:   "file was not accessed — agent may not have known what to change",
			})
		}
	}

	return reports
}

// validateEditor checks that the editor actually modified content files.
// Editors must commit changes (edits in place) — a no-diff result means the agent
// didn't do any editing work.
func validateEditor(worktree, baseBranch, logsDir, taskID string) (*ValidationResult, error) {
	if worktree == "" {
		return &ValidationResult{OK: false, Reason: "editor_no_worktree"}, nil
	}

	// Check for commits beyond the base
	diffCmd := exec.Command("git", "-C", worktree, "diff", "--stat", baseBranch+"..HEAD")
	diffOut, err := diffCmd.Output()
	if err != nil {
		return &ValidationResult{OK: false, Reason: fmt.Sprintf("editor_diff_error: %v", err)}, nil
	}

	diffStr := strings.TrimSpace(string(diffOut))
	if diffStr == "" {
		return &ValidationResult{OK: false, Reason: "editor_no_changes: agent committed no edits"}, nil
	}

	// Check that at least one content file was modified (not just metadata)
	filesCmd := exec.Command("git", "-C", worktree, "diff", "--name-only", baseBranch+"..HEAD")
	filesOut, err := filesCmd.Output()
	if err != nil {
		return &ValidationResult{OK: true, Reason: "editor_diff_exists_but_filelist_error"}, nil
	}

	files := strings.Split(strings.TrimSpace(string(filesOut)), "\n")
	hasContentFile := false
	for _, f := range files {
		if strings.HasSuffix(f, ".md") || strings.HasSuffix(f, ".txt") || strings.HasSuffix(f, ".html") {
			hasContentFile = true
			break
		}
	}

	if !hasContentFile {
		return &ValidationResult{OK: false, Reason: "editor_no_content_files: changes exist but no .md/.txt/.html files modified"}, nil
	}

	return &ValidationResult{OK: true, Reason: "editor_validated"}, nil
}

// validateIllustrator checks that the illustrator produced visual output files.
// Illustrators should commit image files (.png, .svg, .jpg) or the scripts that generate them.
func validateIllustrator(worktree, baseBranch, logsDir, taskID string) (*ValidationResult, error) {
	if worktree == "" {
		return &ValidationResult{OK: false, Reason: "illustrator_no_worktree"}, nil
	}

	filesCmd := exec.Command("git", "-C", worktree, "diff", "--name-only", baseBranch+"..HEAD")
	filesOut, err := filesCmd.Output()
	if err != nil {
		return &ValidationResult{OK: false, Reason: fmt.Sprintf("illustrator_diff_error: %v", err)}, nil
	}

	filesStr := strings.TrimSpace(string(filesOut))
	if filesStr == "" {
		return &ValidationResult{OK: false, Reason: "illustrator_no_changes: agent committed no files"}, nil
	}

	files := strings.Split(filesStr, "\n")
	hasVisual := false
	hasSource := false
	visualExts := map[string]bool{".png": true, ".svg": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".pdf": true}
	sourceExts := map[string]bool{".py": true, ".js": true, ".ts": true, ".html": true, ".d2": true, ".mmd": true}

	for _, f := range files {
		ext := strings.ToLower(filepath.Ext(f))
		if visualExts[ext] {
			hasVisual = true
		}
		if sourceExts[ext] {
			hasSource = true
		}
	}

	if !hasVisual && !hasSource {
		return &ValidationResult{OK: false, Reason: "illustrator_no_visual_output: no image files (.png/.svg) or generation scripts (.py/.html/.d2) committed"}, nil
	}

	return &ValidationResult{OK: true, Reason: "illustrator_validated"}, nil
}
