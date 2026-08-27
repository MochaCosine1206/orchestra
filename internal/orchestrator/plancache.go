package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/db"
)

// CacheHit represents a successful plan cache lookup result.
type CacheHit struct {
	Plan    []DecomposedTask
	Score   float64
	Tier    string
	CacheID int64
}

// stopWords are common English words excluded from keyword sets for Jaccard similarity.
var stopWords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
	"in": true, "on": true, "at": true, "to": true, "for": true, "of": true,
	"with": true, "by": true, "from": true, "is": true, "are": true, "was": true,
	"be": true, "been": true, "being": true, "have": true, "has": true, "had": true,
	"do": true, "does": true, "did": true, "will": true, "would": true, "could": true,
	"should": true, "may": true, "might": true, "shall": true, "can": true,
	"not": true, "no": true, "nor": true, "so": true, "if": true, "then": true,
	"than": true, "that": true, "this": true, "these": true, "those": true,
	"it": true, "its": true, "all": true, "each": true, "every": true,
	"any": true, "both": true, "such": true, "into": true, "through": true,
	"about": true, "up": true, "out": true, "as": true, "which": true, "what": true,
}

// w5h2Actions are recognized primary verbs for W5H2 key extraction.
var w5h2Actions = []string{"fix", "add", "refactor", "remove", "update", "implement", "create", "delete", "migrate", "test"}

// pathPattern matches file/directory/module paths in goal text.
var pathPattern = regexp.MustCompile(`(?:^|\s)((?:[a-zA-Z0-9_\-]+/)+[a-zA-Z0-9_\-]+(?:\.\w+)?|[a-zA-Z0-9_\-]+\.\w+)`)

// NormalizeGoalHash computes SHA-256 of lowercased, whitespace-normalized goal text.
func NormalizeGoalHash(goal string) string {
	normalized := strings.ToLower(strings.TrimSpace(goal))
	normalized = regexp.MustCompile(`\s+`).ReplaceAllString(normalized, " ")
	h := sha256.Sum256([]byte(normalized))
	return fmt.Sprintf("%x", h)
}

// ExtractW5H2Key parses a goal into an (action, target) tuple.
// Action is the primary verb; target is the primary file/directory/module path.
func ExtractW5H2Key(goal string) (action, target string) {
	lower := strings.ToLower(goal)
	words := strings.Fields(lower)

	for _, w := range words {
		for _, a := range w5h2Actions {
			if w == a {
				action = a
				break
			}
		}
		if action != "" {
			break
		}
	}

	matches := pathPattern.FindAllStringSubmatch(goal, -1)
	if len(matches) > 0 {
		target = strings.TrimSpace(matches[0][1])
	}

	return action, target
}

// extractKeywords splits goal text into a lowercase keyword set, removing stop words.
func extractKeywords(goal string) map[string]bool {
	words := strings.Fields(strings.ToLower(goal))
	kw := make(map[string]bool)
	for _, w := range words {
		// Strip common punctuation
		w = strings.Trim(w, ".,;:!?()[]{}\"'`")
		if w == "" || stopWords[w] {
			continue
		}
		kw[w] = true
	}
	return kw
}

// KeywordJaccard computes the Jaccard similarity between two keyword sets.
// Returns 0.0 for disjoint sets and 1.0 for identical sets.
func KeywordJaccard(a, b map[string]bool) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0.0
	}
	intersection := 0
	for k := range a {
		if b[k] {
			intersection++
		}
	}
	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0.0
	}
	return float64(intersection) / float64(union)
}

// tierString converts a ComplexityTier int to a string label.
func tierString(tier int) string {
	switch ComplexityTier(tier) {
	case Tier1Simple:
		return "simple"
	case Tier2Medium:
		return "medium"
	case Tier3Complex:
		return "complex"
	default:
		return fmt.Sprintf("tier-%d", tier)
	}
}

// LookupCache tries 3 tiers in order: (1) exact hash, (2) W5H2 match, (3) keyword Jaccard.
// Returns nil if no cache hit meets the threshold. Cached plan's tier must match estimatedTier.
func (c *Conductor) LookupCache(ctx context.Context, goal string, estimatedTier int) (*CacheHit, error) {
	hash := NormalizeGoalHash(goal)
	c.log("Plan cache lookup: hash=%s tier=%d goal_len=%d goal_prefix=%q", hash[:16], estimatedTier, len(goal), goal[:min(len(goal), 80)])

	// Tier 1: exact hash match (score=1.0)
	entry, err := c.DB.LookupPlanCacheExact(ctx, hash)
	if entry != nil {
		c.log("Plan cache exact: found entry id=%d tier=%v", entry.ID, entry.Tier)
	} else {
		c.log("Plan cache exact: no match for hash=%s (err=%v)", hash[:16], err)
	}
	if err != nil {
		return nil, fmt.Errorf("exact cache lookup: %w", err)
	}
	if entry != nil && tierMatches(entry, estimatedTier) {
		plan, err := parsePlanJSON(entry.PlanJSON)
		if err != nil {
			return nil, fmt.Errorf("parsing cached plan JSON: %w", err)
		}
		return &CacheHit{
			Plan:    plan,
			Score:   1.0,
			Tier:    tierString(int(entry.Tier.Int64)),
			CacheID: entry.ID,
		}, nil
	}

	// Tier 2: W5H2 match (score=0.9)
	action, target := ExtractW5H2Key(goal)
	if action != "" && target != "" {
		w5h2Key := action + ":" + target
		entry, err := c.DB.LookupPlanCacheW5H2(ctx, w5h2Key)
		if err != nil {
			return nil, fmt.Errorf("W5H2 cache lookup: %w", err)
		}
		if entry != nil && tierMatches(entry, estimatedTier) {
			plan, err := parsePlanJSON(entry.PlanJSON)
			if err != nil {
				return nil, fmt.Errorf("parsing cached plan JSON: %w", err)
			}
			return &CacheHit{
				Plan:    plan,
				Score:   0.9,
				Tier:    tierString(int(entry.Tier.Int64)),
				CacheID: entry.ID,
			}, nil
		}
	}

	// Tier 3: keyword Jaccard (threshold >= 0.6)
	goalKW := extractKeywords(goal)
	entries, err := c.DB.LookupPlanCacheKeyword(ctx)
	if err != nil {
		return nil, fmt.Errorf("keyword cache lookup: %w", err)
	}

	var bestEntry *struct {
		plan    []DecomposedTask
		score   float64
		tier    int64
		cacheID int64
	}
	for _, e := range entries {
		if !tierMatches(&e, estimatedTier) {
			continue
		}
		if !e.Keywords.Valid || e.Keywords.String == "" {
			continue
		}
		entryKW := keywordsFromString(e.Keywords.String)
		score := KeywordJaccard(goalKW, entryKW)
		if score >= 0.6 && (bestEntry == nil || score > bestEntry.score) {
			plan, pErr := parsePlanJSON(e.PlanJSON)
			if pErr != nil {
				continue
			}
			bestEntry = &struct {
				plan    []DecomposedTask
				score   float64
				tier    int64
				cacheID int64
			}{plan, score, e.Tier.Int64, e.ID}
		}
	}
	if bestEntry != nil {
		return &CacheHit{
			Plan:    bestEntry.plan,
			Score:   bestEntry.score,
			Tier:    tierString(int(bestEntry.tier)),
			CacheID: bestEntry.cacheID,
		}, nil
	}

	return nil, nil
}

// StoreCache computes hash, W5H2 key, keywords, and action-based TTL, then stores the plan.
func (c *Conductor) StoreCache(ctx context.Context, goal string, plan []DecomposedTask, tier int) error {
	hash := NormalizeGoalHash(goal)
	action, target := ExtractW5H2Key(goal)
	w5h2Key := ""
	if action != "" && target != "" {
		w5h2Key = action + ":" + target
	}

	kw := extractKeywords(goal)
	kwList := make([]string, 0, len(kw))
	for k := range kw {
		kwList = append(kwList, k)
	}
	keywords := strings.Join(kwList, " ")

	ttlDays := actionTTL(action)

	planJSON, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("marshaling plan to JSON: %w", err)
	}

	// Build file_mtimes from files referenced in the plan
	fileMtimes := buildFileMtimes(c.RepoRoot, plan)

	return c.DB.StorePlanCache(ctx, hash, goal, w5h2Key, keywords, string(planJSON), action, tier, ttlDays, fileMtimes)
}

// InvalidateStaleEntries checks file_mtimes JSON against current filesystem and invalidates
// entries where referenced files have changed.
func (c *Conductor) InvalidateStaleEntries(ctx context.Context) error {
	entries, err := c.DB.LookupPlanCacheKeyword(ctx)
	if err != nil {
		return fmt.Errorf("loading plan cache entries for invalidation: %w", err)
	}

	for _, e := range entries {
		if !e.FileMtimes.Valid || e.FileMtimes.String == "" {
			continue
		}
		// Skip invalidation for entries less than 1 hour old — mtime changes from
		// git operations (merge, worktree cleanup) shouldn't invalidate fresh plans
		if !e.CreatedAt.IsZero() && time.Since(e.CreatedAt) < time.Hour {
			continue
		}
		var mtimes map[string]string
		if err := json.Unmarshal([]byte(e.FileMtimes.String), &mtimes); err != nil {
			// Corrupt mtimes — invalidate the entry
			c.DB.InvalidatePlanCache(ctx, e.GoalHash)
			continue
		}
		if isStale(c.RepoRoot, mtimes) {
			c.log("Plan cache invalidated (stale files): hash=%s", e.GoalHash[:16])
			c.DB.InvalidatePlanCache(ctx, e.GoalHash)
		}
	}
	return nil
}

// actionTTL returns TTL in days based on action type.
func actionTTL(action string) int {
	switch action {
	case "fix":
		return 7
	case "add", "refactor":
		return 14
	case "docs":
		return 30
	default:
		return 14
	}
}

// tierMatches checks if a cache entry's tier matches the estimated tier.
func tierMatches(entry *db.PlanCacheEntry, estimatedTier int) bool {
	if !entry.Tier.Valid {
		return true // no tier stored, match anything
	}
	return int(entry.Tier.Int64) == estimatedTier
}

// parsePlanJSON deserializes a JSON array of DecomposedTask.
func parsePlanJSON(planJSON string) ([]DecomposedTask, error) {
	var plan []DecomposedTask
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		return nil, err
	}
	return plan, nil
}

// keywordsFromString converts a space-separated keyword string to a set.
func keywordsFromString(s string) map[string]bool {
	words := strings.Fields(s)
	kw := make(map[string]bool, len(words))
	for _, w := range words {
		kw[w] = true
	}
	return kw
}

// buildFileMtimes collects modification times for all files referenced in the plan.
func buildFileMtimes(repoRoot string, plan []DecomposedTask) string {
	mtimes := make(map[string]string)
	for _, task := range plan {
		for _, f := range task.Files {
			fullPath := f
			if repoRoot != "" && !strings.HasPrefix(f, "/") {
				fullPath = repoRoot + "/" + f
			}
			info, err := os.Stat(fullPath)
			if err != nil {
				continue
			}
			mtimes[f] = info.ModTime().UTC().Format(time.RFC3339)
		}
	}
	if len(mtimes) == 0 {
		return ""
	}
	data, _ := json.Marshal(mtimes)
	return string(data)
}

// isStale checks if any file in the mtime map has been modified since the recorded time.
func isStale(repoRoot string, mtimes map[string]string) bool {
	for f, recorded := range mtimes {
		fullPath := f
		if repoRoot != "" && !strings.HasPrefix(f, "/") {
			fullPath = repoRoot + "/" + f
		}
		info, err := os.Stat(fullPath)
		if err != nil {
			// File was deleted — stale
			return true
		}
		recordedTime, err := time.Parse(time.RFC3339, recorded)
		if err != nil {
			return true
		}
		if info.ModTime().UTC().After(recordedTime) {
			return true
		}
	}
	return false
}
