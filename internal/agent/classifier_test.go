package agent

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "stderr.txt")
	os.WriteFile(f, []byte(content), 0o644)
	return f
}

func TestClassifyNormalFailure(t *testing.T) {
	f := writeTemp(t, "some random error output\nexit code 1")
	ft := ClassifyFailure(f, "")
	if ft.Kind != "normal_failure" {
		t.Errorf("expected normal_failure, got %s", ft.Kind)
	}
}

func TestClassifyEmptyFile(t *testing.T) {
	f := writeTemp(t, "")
	ft := ClassifyFailure(f, "")
	if ft.Kind != "normal_failure" {
		t.Errorf("expected normal_failure for empty, got %s", ft.Kind)
	}
}

func TestClassifyMissingFile(t *testing.T) {
	ft := ClassifyFailure("/nonexistent/path/stderr.txt", "")
	if ft.Kind != "normal_failure" {
		t.Errorf("expected normal_failure for missing file, got %s", ft.Kind)
	}
}

func TestClassifyEmptyPaths(t *testing.T) {
	ft := ClassifyFailure("", "")
	if ft.Kind != "normal_failure" {
		t.Errorf("expected normal_failure for empty paths, got %s", ft.Kind)
	}
}

func TestClassifyRateLimit429(t *testing.T) {
	f := writeTemp(t, "Error: 429 Too Many Requests\nRetry after 30 seconds")
	ft := ClassifyFailure(f, "")
	if ft.Kind != "rate_limit" {
		t.Errorf("expected rate_limit, got %s", ft.Kind)
	}
	if ft.WaitSeconds != 30 {
		t.Errorf("expected 30 seconds, got %d", ft.WaitSeconds)
	}
}

func TestClassifyRateLimitError(t *testing.T) {
	f := writeTemp(t, `{"type":"error","error":{"type":"rate_limit_error","message":"Rate limit exceeded"}}`)
	ft := ClassifyFailure(f, "")
	if ft.Kind != "rate_limit" {
		t.Errorf("expected rate_limit, got %s", ft.Kind)
	}
}

func TestClassifyRateLimitDefaultWait(t *testing.T) {
	f := writeTemp(t, "rate limit exceeded")
	ft := ClassifyFailure(f, "")
	if ft.Kind != "rate_limit" {
		t.Errorf("expected rate_limit, got %s", ft.Kind)
	}
	if ft.WaitSeconds != 60 {
		t.Errorf("expected default 60 seconds, got %d", ft.WaitSeconds)
	}
}

func TestClassifySessionLimit(t *testing.T) {
	f := writeTemp(t, "You've hit your usage limit. Your limit will reset in 30 minutes.")
	ft := ClassifyFailure(f, "")
	if ft.Kind != "session_limit" {
		t.Errorf("expected session_limit, got %s", ft.Kind)
	}
	// Reset should be ~30min from now
	expected := time.Now().Unix() + 30*60
	if abs(ft.ResetEpoch-expected) > 5 {
		t.Errorf("reset epoch %d not within 5s of expected %d", ft.ResetEpoch, expected)
	}
}

func TestClassifySessionLimitQuotaExceeded(t *testing.T) {
	f := writeTemp(t, "quota exceeded for this billing period")
	ft := ClassifyFailure(f, "")
	if ft.Kind != "session_limit" {
		t.Errorf("expected session_limit, got %s", ft.Kind)
	}
}

func TestClassifyContextExhausted(t *testing.T) {
	f := writeTemp(t, "Error: context window exceeded, maximum context length reached")
	ft := ClassifyFailure(f, "")
	if ft.Kind != "context_exhausted" {
		t.Errorf("expected context_exhausted, got %s", ft.Kind)
	}
}

func TestClassifyContextCompaction(t *testing.T) {
	f := writeTemp(t, "compaction failed: could not reduce context below token limit")
	ft := ClassifyFailure(f, "")
	if ft.Kind != "context_exhausted" {
		t.Errorf("expected context_exhausted, got %s", ft.Kind)
	}
}

func TestClassifyContextPromptTooLong(t *testing.T) {
	f := writeTemp(t, "Error: Prompt is too long. Max tokens: 200000, current: 210543")
	ft := ClassifyFailure(f, "")
	if ft.Kind != "context_exhausted" {
		t.Errorf("expected context_exhausted for 'Prompt is too long', got %s", ft.Kind)
	}
}

func TestClassifyContextPromptTooLongJSONL(t *testing.T) {
	stderrFile := writeTemp(t, "")
	jsonlDir := t.TempDir()
	jsonlFile := filepath.Join(jsonlDir, "log.jsonl")
	os.WriteFile(jsonlFile, []byte(`{"type":"error","error":{"type":"prompt_too_long","message":"prompt is too long"}}
`), 0o644)
	ft := ClassifyFailure(stderrFile, jsonlFile)
	if ft.Kind != "context_exhausted" {
		t.Errorf("expected context_exhausted from JSONL prompt_too_long, got %s", ft.Kind)
	}
}

func TestClassifyFromJSONL(t *testing.T) {
	// Empty stderr but rate limit in JSONL
	stderrFile := writeTemp(t, "")
	jsonlDir := t.TempDir()
	jsonlFile := filepath.Join(jsonlDir, "log.jsonl")
	os.WriteFile(jsonlFile, []byte(`{"type":"error"}
{"type":"error","error":{"type":"rate_limit_error","message":"too many requests, retry after 45 seconds"}}
`), 0o644)
	ft := ClassifyFailure(stderrFile, jsonlFile)
	if ft.Kind != "rate_limit" {
		t.Errorf("expected rate_limit from JSONL, got %s", ft.Kind)
	}
	if ft.WaitSeconds != 45 {
		t.Errorf("expected 45 seconds from JSONL, got %d", ft.WaitSeconds)
	}
}

func TestExtractWaitSecondsRetryAfter(t *testing.T) {
	s := extractWaitSeconds("Retry-After: 120")
	if s != 120 {
		t.Errorf("expected 120, got %d", s)
	}
}

func TestExtractWaitSecondsTryAgainIn(t *testing.T) {
	s := extractWaitSeconds("Please try again in 90 seconds")
	if s != 90 {
		t.Errorf("expected 90, got %d", s)
	}
}

func TestExtractResetTimeISO(t *testing.T) {
	epoch := extractResetTime("Your limit will reset 2026-03-15T10:00:00")
	expected, _ := time.Parse("2006-01-02T15:04:05", "2026-03-15T10:00:00")
	if epoch != expected.Unix() {
		t.Errorf("expected %d, got %d", expected.Unix(), epoch)
	}
}

func TestExtractResetTimeDefault(t *testing.T) {
	epoch := extractResetTime("some error with no time info")
	expected := time.Now().Unix() + 1800
	if abs(epoch-expected) > 5 {
		t.Errorf("expected ~%d (now+1800), got %d", expected, epoch)
	}
}

func TestResetHourAM(t *testing.T) {
	epoch := extractResetTime("limit resets 10am Eastern")
	now := time.Now()
	target := time.Date(now.Year(), now.Month(), now.Day(), 10, 0, 0, 0, now.Location())
	if target.Before(now) {
		target = target.Add(24 * time.Hour)
	}
	if abs(epoch-target.Unix()) > 5 {
		t.Errorf("expected ~%d, got %d", target.Unix(), epoch)
	}
}

func TestDetectRateLimitEvent_Rejected(t *testing.T) {
	dir := t.TempDir()
	jsonlFile := filepath.Join(dir, "agent.jsonl")
	content := `{"type":"init","session_id":"abc123"}
{"type":"system","message":"starting"}
{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1772902800,"rateLimitType":"five_hour"}}
{"type":"result","subtype":"success","is_error":true,"result":"You've hit your limit"}
`
	os.WriteFile(jsonlFile, []byte(content), 0o644)

	ft := DetectRateLimitEvent(jsonlFile)
	if ft == nil {
		t.Fatal("expected non-nil FailureType for rejected rate_limit_event")
	}
	if ft.Kind != "session_limit" {
		t.Errorf("expected session_limit, got %s", ft.Kind)
	}
	if ft.ResetEpoch != 1772902800 {
		t.Errorf("expected ResetEpoch 1772902800, got %d", ft.ResetEpoch)
	}
}

func TestDetectRateLimitEvent_AllowedWarning(t *testing.T) {
	dir := t.TempDir()
	jsonlFile := filepath.Join(dir, "agent.jsonl")
	// allowed_warning means the request was served — not rate limited.
	content := `{"type":"init","session_id":"abc123"}
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","resetsAt":1772902800,"rateLimitType":"five_hour"}}
{"type":"result","subtype":"success","result":"all done"}
`
	os.WriteFile(jsonlFile, []byte(content), 0o644)

	ft := DetectRateLimitEvent(jsonlFile)
	if ft != nil {
		t.Errorf("expected nil for allowed_warning, got %+v", ft)
	}
}

func TestClassifyFailure_RateLimitEventInJsonl(t *testing.T) {
	stderrFile := writeTemp(t, "") // empty stderr
	dir := t.TempDir()
	jsonlFile := filepath.Join(dir, "agent.jsonl")
	content := `{"type":"init","session_id":"abc123"}
{"type":"rate_limit_event","rate_limit_info":{"status":"rejected","resetsAt":1772902800,"rateLimitType":"five_hour"}}
{"type":"result","subtype":"success","is_error":true,"result":"You've hit your limit"}
`
	os.WriteFile(jsonlFile, []byte(content), 0o644)

	ft := ClassifyFailure(stderrFile, jsonlFile)
	if ft.Kind != "session_limit" {
		t.Errorf("expected session_limit from ClassifyFailure with rate_limit_event, got %s", ft.Kind)
	}
	if ft.ResetEpoch != 1772902800 {
		t.Errorf("expected ResetEpoch 1772902800, got %d", ft.ResetEpoch)
	}
}

func abs(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}
