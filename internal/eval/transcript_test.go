package eval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTaskTranscript(t *testing.T) {
	dir := t.TempDir()
	taskID := "t-test-001"
	logFile := filepath.Join(dir, taskID+".jsonl")

	// Write a realistic JSONL log
	content := `{"type":"system","subtype":"init","model":"claude-opus-4-6"}
{"type":"assistant","message":{"content":[{"type":"text","text":"I'll read the file first."},{"type":"tool_use","name":"Read","input":{}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"package main\nfunc main() {}"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"The file looks good. I'll add the function now."}]}}
{"type":"result","subtype":"success","is_error":false}
`
	os.WriteFile(logFile, []byte(content), 0o644)

	transcript, err := ReadTaskTranscript(dir, taskID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should contain assistant text, tool_use, tool_result, and result
	if transcript == "" {
		t.Fatal("transcript should not be empty")
	}
	if !contains(transcript, "I'll read the file first.") {
		t.Error("transcript should contain assistant text")
	}
	if !contains(transcript, "[tool_use] Read") {
		t.Error("transcript should contain tool use")
	}
	if !contains(transcript, "[tool_result]") {
		t.Error("transcript should contain tool result")
	}
	if !contains(transcript, "[result] subtype=success") {
		t.Error("transcript should contain result")
	}
}

func TestReadTaskTranscriptMissing(t *testing.T) {
	dir := t.TempDir()
	_, err := ReadTaskTranscript(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestReadTaskTranscriptEmpty(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "t-empty.jsonl"), []byte(""), 0o644)
	_, err := ReadTaskTranscript(dir, "t-empty")
	if err == nil {
		t.Fatal("expected error for empty transcript")
	}
}

func TestSyntheticTranscript(t *testing.T) {
	s := Scenario{ID: "test-1", Role: "implementer", Goal: "add func", ExpectedOutcome: "func added"}
	result := SyntheticTranscript(s, "v1")
	if result == "" {
		t.Fatal("synthetic transcript should not be empty")
	}
	if !contains(result, "implementer") {
		t.Error("should contain role")
	}
	if !contains(result, "v1") {
		t.Error("should contain version")
	}
}
