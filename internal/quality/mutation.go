package quality

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	// DefaultMutationThreshold is the minimum mutation score for the gate to pass.
	DefaultMutationThreshold = 0.80

	// humanTestManifestPath is the relative path to the human-written test manifest.
	humanTestManifestPath = ".quality/human-tests.manifest"
)

// MutationGate runs go-mutesting and evaluates the mutation score.
// It computes two scores:
//   - FullScore: killed/total across all mutants
//   - BaselineScore: killed/total for mutants in files listed in the human-tests manifest
//
// The gate fails if FullScore < DefaultMutationThreshold or if BaselineScore drops
// below the stored mutation_threshold in SQLite.
type MutationGate struct {
	Runner    CommandRunner
	Logger    *slog.Logger
	Threshold float64 // Override for DefaultMutationThreshold; 0 means use default
}

// Name returns the gate identifier.
func (g *MutationGate) Name() string { return "mutation" }

// Check runs go-mutesting and parses the mutation score.
func (g *MutationGate) Check(ctx context.Context, projectPath string, _ string) (*LayerResult, error) {
	start := time.Now()
	g.Logger.Info("running mutation gate", "project", projectPath)

	threshold := g.Threshold
	if threshold == 0 {
		threshold = DefaultMutationThreshold
	}

	out, err := g.Runner.RunDir(projectPath, "go-mutesting", "./...")
	duration := time.Since(start)
	output := string(out)

	result := &LayerResult{
		Layer:    g.Name(),
		Duration: duration,
	}

	// go-mutesting exits non-zero when mutants survive — parse output regardless.
	fullScore, killed, total := parseMutationScore(output)

	// Load human-test manifest for baseline scoring.
	manifestPath := filepath.Join(projectPath, humanTestManifestPath)
	baselineScore := computeBaselineScore(output, manifestPath)

	result.Score = fullScore

	if err != nil && total == 0 {
		// Genuine execution failure (not just survived mutants).
		result.Passed = false
		result.Details = fmt.Sprintf("go-mutesting failed to run: %s", output)
		return result, nil
	}

	details := fmt.Sprintf("mutation score: %.2f (%d/%d killed), baseline score: %.2f",
		fullScore, killed, total, baselineScore)

	if fullScore < threshold {
		result.Passed = false
		result.Details = fmt.Sprintf("%s — below threshold %.2f", details, threshold)
		g.Logger.Warn("mutation gate failed",
			"full_score", fullScore,
			"baseline_score", baselineScore,
			"threshold", threshold,
		)
		return result, nil
	}

	result.Passed = true
	result.Details = details
	g.Logger.Info("mutation gate passed",
		"full_score", fullScore,
		"baseline_score", baselineScore,
		"killed", killed,
		"total", total,
	)
	return result, nil
}

// mutationScoreRe matches go-mutesting summary output like "The mutation score is 0.8500 (17 killed, 3 survived, 20 total)".
var mutationScoreRe = regexp.MustCompile(
	`mutation score is ([\d.]+)\s+\((\d+)\s+killed,\s+\d+\s+survived,\s+(\d+)\s+total\)`,
)

// parseMutationScore extracts the mutation score and kill counts from go-mutesting output.
func parseMutationScore(output string) (score float64, killed int, total int) {
	matches := mutationScoreRe.FindStringSubmatch(output)
	if len(matches) < 4 {
		return 0.0, 0, 0
	}

	score, _ = strconv.ParseFloat(matches[1], 64)
	killed, _ = strconv.Atoi(matches[2])
	total, _ = strconv.Atoi(matches[3])
	return score, killed, total
}

// mutantFileRe matches go-mutesting per-file lines like "KILLED: /path/to/file.go:10".
var mutantFileRe = regexp.MustCompile(`^(KILLED|SURVIVED):\s+(.+?):\d+`)

// computeBaselineScore calculates mutation score for files listed in the human-tests manifest.
// If the manifest doesn't exist, returns 0.0 (no baseline protection).
func computeBaselineScore(output string, manifestPath string) float64 {
	manifest, err := loadManifest(manifestPath)
	if err != nil || len(manifest) == 0 {
		return 0.0
	}

	var baseKilled, baseTotal int
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		matches := mutantFileRe.FindStringSubmatch(line)
		if len(matches) < 3 {
			continue
		}
		status := matches[1]
		filePath := matches[2]

		if !isInManifest(filePath, manifest) {
			continue
		}

		baseTotal++
		if status == "KILLED" {
			baseKilled++
		}
	}

	if baseTotal == 0 {
		return 0.0
	}
	return float64(baseKilled) / float64(baseTotal)
}

// loadManifest reads the human-tests manifest file (one file path per line).
func loadManifest(path string) (map[string]bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening manifest %s: %w", path, err)
	}
	defer f.Close()

	manifest := make(map[string]bool)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		manifest[line] = true
	}
	return manifest, scanner.Err()
}

// isInManifest checks whether a file path matches any entry in the manifest.
// It checks both exact match and suffix match (for relative vs absolute paths).
func isInManifest(filePath string, manifest map[string]bool) bool {
	if manifest[filePath] {
		return true
	}
	for entry := range manifest {
		if strings.HasSuffix(filePath, "/"+entry) || strings.HasSuffix(filePath, entry) {
			return true
		}
	}
	return false
}
