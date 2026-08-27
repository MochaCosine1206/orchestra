package agent

import (
	"encoding/json"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/MochaCosine1206/orchestra/internal/healing"
)

// FailureType describes the classification of an agent failure.
type FailureType struct {
	Kind        string // "normal_failure", "rate_limit", "session_limit", "context_exhausted"
	WaitSeconds int    // seconds to wait before retry (rate_limit, session_limit)
	ResetEpoch  int64  // epoch when limit resets (session_limit)
}

// Pattern regexes (compiled once).
var (
	ratePattern    = regexp.MustCompile(`(?i)429|rate.?limit|too many requests|rate_limit_error`)
	sessionPattern = regexp.MustCompile(`(?i)limit will reset|usage.?limit|session.?limit|quota.?exceed|billing.?limit|hit.?your.?limit`)
	contextPattern       = regexp.MustCompile(`(?i)context.?limit|context.?window|compaction|maximum.?context|token.?limit|context.?length|prompt.*(is|too).*long|prompt_too_long`)
	contentFilterPattern = regexp.MustCompile(`(?i)content.?filter|blocked by content|output.?blocked|safety.?filter`)

	waitSecondsPattern = regexp.MustCompile(`(?i)(retry[- ]?after|try again in|wait)[: ]*(\d+)`)
	genericSeconds     = regexp.MustCompile(`(?i)(\d+)\s*seconds?`)
	isoTimestamp       = regexp.MustCompile(`(?i)reset[^0-9]*(\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}(:\d{2})?)`)
	inMinutes          = regexp.MustCompile(`(?i)in (\d+) minutes?`)
	resetHour          = regexp.MustCompile(`(?i)resets?\s+(\d{1,2})(am|pm)`)
)

// DetectRateLimitEvent scans a full JSONL log for a rate_limit_event with
// status:"rejected". Returns a session_limit FailureType with the exact
// resetsAt epoch, or nil if no rejected rate limit event was found.
// G139: This gives us the precise reset time from structured JSON instead of
// parsing human-readable "resets 12pm" text with regex.
func DetectRateLimitEvent(jsonlPath string) *FailureType {
	if jsonlPath == "" {
		return nil
	}
	data, err := os.ReadFile(jsonlPath)
	if err != nil {
		return nil
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.Contains(line, `"rate_limit_event"`) {
			continue
		}

		var event struct {
			Type          string `json:"type"`
			RateLimitInfo struct {
				Status        string `json:"status"`
				ResetsAt      int64  `json:"resetsAt"`
				RateLimitType string `json:"rateLimitType"`
			} `json:"rate_limit_info"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		if event.Type != "rate_limit_event" || event.RateLimitInfo.Status != "rejected" {
			continue
		}

		resetEpoch := event.RateLimitInfo.ResetsAt
		wait := int(resetEpoch - time.Now().Unix())
		if wait < 0 {
			wait = 0
		}
		return &FailureType{
			Kind:        "session_limit",
			WaitSeconds: wait,
			ResetEpoch:  resetEpoch,
		}
	}
	return nil
}

// ClassifyFailure reads an agent's stderr file (and optionally JSONL log) and
// classifies the failure type.
func ClassifyFailure(stderrPath, jsonlPath string) FailureType {
	// G139: Check for structured rate_limit_event first — gives exact resetsAt epoch.
	if ft := DetectRateLimitEvent(jsonlPath); ft != nil {
		return *ft
	}

	content := readFileContent(stderrPath)

	// Also check JSONL log tail (Claude Max rate limits appear in stream-json, not stderr)
	if jsonlPath != "" {
		jsonlContent := readFileTail(jsonlPath, 10)
		if jsonlContent != "" {
			content = content + "\n" + jsonlContent
		}
	}

	if content == "" {
		return FailureType{Kind: "normal_failure"}
	}

	// Rate limit detection
	if ratePattern.MatchString(content) {
		wait := extractWaitSeconds(content)
		return FailureType{Kind: "rate_limit", WaitSeconds: wait}
	}

	// Session/usage limit detection
	if sessionPattern.MatchString(content) {
		epoch := extractResetTime(content)
		wait := int(epoch - time.Now().Unix())
		if wait < 0 {
			wait = 0
		}
		return FailureType{Kind: "session_limit", WaitSeconds: wait, ResetEpoch: epoch}
	}

	// Content filter detection — transient, should retry (does NOT count toward circuit breaker)
	if contentFilterPattern.MatchString(content) {
		return FailureType{Kind: "content_filter", WaitSeconds: 5}
	}

	// Context exhaustion detection
	if contextPattern.MatchString(content) {
		return FailureType{Kind: "context_exhausted"}
	}

	// Extended classification: if stderr contains parseable build errors,
	// upgrade from normal_failure to build_failure so healing can attempt a fix.
	if buildErrors := healing.ParseBuildError(content); len(buildErrors) > 0 {
		return FailureType{Kind: "build_failure"}
	}

	return FailureType{Kind: "normal_failure"}
}

// extractWaitSeconds tries to parse a wait duration from rate-limit error text.
// Returns 60 as default.
func extractWaitSeconds(content string) int {
	// "retry after N seconds" / "retry-after: N" / "try again in N seconds"
	if m := waitSecondsPattern.FindStringSubmatch(content); len(m) >= 3 {
		if n, err := strconv.Atoi(m[2]); err == nil {
			return n
		}
	}

	// "N seconds" anywhere
	if m := genericSeconds.FindStringSubmatch(content); len(m) >= 2 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return n
		}
	}

	return 60 // default
}

// extractResetTime tries to parse a reset timestamp from session-limit error text.
// Returns now + 1800 (30 minutes) as default.
func extractResetTime(content string) int64 {
	now := time.Now()

	// ISO-8601 timestamp near "reset"
	if m := isoTimestamp.FindStringSubmatch(content); len(m) >= 2 {
		ts := m[1]
		for _, layout := range []string{
			"2006-01-02T15:04:05",
			"2006-01-02 15:04:05",
			"2006-01-02T15:04",
			"2006-01-02 15:04",
		} {
			if t, err := time.Parse(layout, ts); err == nil {
				return t.Unix()
			}
		}
	}

	// "in N minutes" pattern
	if m := inMinutes.FindStringSubmatch(content); len(m) >= 2 {
		if n, err := strconv.Atoi(m[1]); err == nil {
			return now.Unix() + int64(n*60)
		}
	}

	// "resets Xam/Xpm" pattern
	if m := resetHour.FindStringSubmatch(content); len(m) >= 3 {
		h, err := strconv.Atoi(m[1])
		if err == nil {
			ampm := strings.ToLower(m[2])
			if ampm == "pm" && h < 12 {
				h += 12
			} else if ampm == "am" && h == 12 {
				h = 0
			}
			target := time.Date(now.Year(), now.Month(), now.Day(), h, 0, 0, 0, now.Location())
			if target.Before(now) {
				target = target.Add(24 * time.Hour)
			}
			return target.Unix()
		}
	}

	// Default: now + 30 minutes
	return now.Unix() + 1800
}

func readFileContent(path string) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func readFileTail(path string, maxLines int) string {
	if path == "" {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	if len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
	}
	return strings.Join(lines, "\n")
}
