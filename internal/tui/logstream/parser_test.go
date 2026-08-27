package logstream

import (
	"strings"
	"testing"
)

func TestParseInit(t *testing.T) {
	raw := `{"type":"system","subtype":"init","session_id":"abc-123"}`
	e := ParseLine(raw)
	if e.Kind != KindInit {
		t.Fatalf("expected KindInit, got %d", e.Kind)
	}
	if e.SessionID != "abc-123" {
		t.Fatalf("expected session_id abc-123, got %q", e.SessionID)
	}
}

func TestParseText(t *testing.T) {
	raw := `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"text","text":"I'll analyze the code..."}]}}`
	e := ParseLine(raw)
	if e.Kind != KindText {
		t.Fatalf("expected KindText, got %d", e.Kind)
	}
	if e.Text != "I'll analyze the code..." {
		t.Fatalf("unexpected text: %q", e.Text)
	}
}

func TestParseToolUse(t *testing.T) {
	raw := `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"tool_use","name":"Read","id":"toolu_123","input":{"file_path":"internal/tui/model.go"}}]}}`
	e := ParseLine(raw)
	if e.Kind != KindToolUse {
		t.Fatalf("expected KindToolUse, got %d", e.Kind)
	}
	if e.ToolName != "Read" {
		t.Fatalf("expected tool name Read, got %q", e.ToolName)
	}
	if e.ToolID != "toolu_123" {
		t.Fatalf("expected tool id toolu_123, got %q", e.ToolID)
	}
	if e.ToolInput != "internal/tui/model.go" {
		t.Fatalf("expected input path, got %q", e.ToolInput)
	}
}

func TestParseToolUseBash(t *testing.T) {
	raw := `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"tool_use","name":"Bash","id":"toolu_456","input":{"command":"go build ./..."}}]}}`
	e := ParseLine(raw)
	if e.Kind != KindToolUse {
		t.Fatalf("expected KindToolUse, got %d", e.Kind)
	}
	if e.ToolName != "Bash" {
		t.Fatalf("expected Bash, got %q", e.ToolName)
	}
	if e.ToolInput != "go build ./..." {
		t.Fatalf("expected command, got %q", e.ToolInput)
	}
}

func TestParseToolUseGrep(t *testing.T) {
	raw := `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"tool_use","name":"Grep","id":"toolu_789","input":{"pattern":"func main","path":"cmd/"}}]}}`
	e := ParseLine(raw)
	if e.Kind != KindToolUse {
		t.Fatalf("expected KindToolUse, got %d", e.Kind)
	}
	if e.ToolInput != "func main in cmd/" {
		t.Fatalf("expected pattern+path, got %q", e.ToolInput)
	}
}

func TestParseToolResult(t *testing.T) {
	raw := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_123","type":"tool_result","content":"some file content here"}]}}`
	e := ParseLine(raw)
	if e.Kind != KindToolResult {
		t.Fatalf("expected KindToolResult, got %d", e.Kind)
	}
	if e.ToolID != "toolu_123" {
		t.Fatalf("expected toolu_123, got %q", e.ToolID)
	}
	if e.IsError {
		t.Fatal("expected no error")
	}
}

func TestParseToolResultError(t *testing.T) {
	raw := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_err","type":"tool_result","content":"permission denied","is_error":true}]}}`
	e := ParseLine(raw)
	if e.Kind != KindToolResult {
		t.Fatalf("expected KindToolResult, got %d", e.Kind)
	}
	if !e.IsError {
		t.Fatal("expected error flag")
	}
	if e.ToolResult != "permission denied" {
		t.Fatalf("expected error text, got %q", e.ToolResult)
	}
}

func TestParseResultSuccess(t *testing.T) {
	raw := `{"type":"result","subtype":"success","result":"All done."}`
	e := ParseLine(raw)
	if e.Kind != KindResult {
		t.Fatalf("expected KindResult, got %d", e.Kind)
	}
	if !e.Success {
		t.Fatal("expected success=true")
	}
	if e.ResultText != "All done." {
		t.Fatalf("unexpected result text: %q", e.ResultText)
	}
}

func TestParseResultError(t *testing.T) {
	raw := `{"type":"result","subtype":"error","result":"Something went wrong"}`
	e := ParseLine(raw)
	if e.Kind != KindResult {
		t.Fatalf("expected KindResult, got %d", e.Kind)
	}
	if e.Success {
		t.Fatal("expected success=false")
	}
}

func TestParseGarbage(t *testing.T) {
	raw := "this is not json at all"
	e := ParseLine(raw)
	if e.Kind != KindUnknown {
		t.Fatalf("expected KindUnknown, got %d", e.Kind)
	}
	if e.Raw != raw {
		t.Fatalf("expected raw preserved, got %q", e.Raw)
	}
}

func TestParseEmptyJSON(t *testing.T) {
	raw := `{}`
	e := ParseLine(raw)
	if e.Kind != KindUnknown {
		t.Fatalf("expected KindUnknown for empty object, got %d", e.Kind)
	}
}

func TestParseTruncation(t *testing.T) {
	// Tool inputs/results truncate at maxFieldLen (200)
	longText := strings.Repeat("a", 300)
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","id":"x","input":{"file_path":"` + longText + `"}}]}}`
	e := ParseLine(raw)
	if len(e.ToolInput) > maxFieldLen {
		t.Fatalf("expected tool input truncation to %d chars, got %d", maxFieldLen, len(e.ToolInput))
	}

	// Result text has a higher limit (2000) for multi-line rendering
	longResult := strings.Repeat("a", 2500)
	raw2 := `{"type":"result","subtype":"success","result":"` + longResult + `"}`
	e2 := ParseLine(raw2)
	if e2.Kind != KindResult {
		t.Fatalf("expected KindResult, got %d", e2.Kind)
	}
	if len(e2.ResultText) > 2000 {
		t.Fatalf("expected result truncation to 2000 chars, got %d", len(e2.ResultText))
	}
	if !strings.HasSuffix(e2.ResultText, "...") {
		t.Fatal("expected truncated text to end with ...")
	}
}

func TestParseToolResultContentBlocks(t *testing.T) {
	raw := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_x","type":"tool_result","content":[{"type":"text","text":"hello world"}]}]}}`
	e := ParseLine(raw)
	if e.Kind != KindToolResult {
		t.Fatalf("expected KindToolResult, got %d", e.Kind)
	}
	// Should contain size and content preview
	if !strings.Contains(e.ToolResult, "11 B") {
		t.Fatalf("expected byte count in summary, got %q", e.ToolResult)
	}
	if !strings.Contains(e.ToolResult, "hello world") {
		t.Fatalf("expected content preview in summary, got %q", e.ToolResult)
	}
}

func TestParseRealAssistantLine(t *testing.T) {
	// Realistic line from actual stream-json output
	raw := `{"type":"assistant","message":{"model":"claude-opus-4-6","id":"msg_01X","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_01TEg","name":"Read","input":{"file_path":"/some/path.ts"}}],"stop_reason":null},"session_id":"047a"}`
	e := ParseLine(raw)
	if e.Kind != KindToolUse {
		t.Fatalf("expected KindToolUse, got %d", e.Kind)
	}
	if e.ToolName != "Read" {
		t.Fatalf("expected Read, got %q", e.ToolName)
	}
	if e.ToolInput != "/some/path.ts" {
		t.Fatalf("expected path, got %q", e.ToolInput)
	}
}

func TestParseRealUserLine(t *testing.T) {
	raw := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01W2","type":"tool_result","content":"Todos have been modified successfully."}]},"session_id":"047a"}`
	e := ParseLine(raw)
	if e.Kind != KindToolResult {
		t.Fatalf("expected KindToolResult, got %d", e.Kind)
	}
	if e.ToolID != "toolu_01W2" {
		t.Fatalf("expected tool ID, got %q", e.ToolID)
	}
	if e.ToolResult != "Todos have been modified successfully." {
		t.Fatalf("expected result text, got %q", e.ToolResult)
	}
}

func TestIsDiffContent(t *testing.T) {
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"added line", "+new line", true},
		{"removed line", "-old line", true},
		{"chunk header", "@@ -1,3 +1,4 @@", true},
		{"context only", " unchanged line\n another", false},
		{"empty", "", false},
		{"no diff markers", "hello world\nfoo bar", false},
		{"mixed diff", " context\n+added\n-removed", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := IsDiffContent(tc.text)
			if got != tc.want {
				t.Errorf("IsDiffContent(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestFormatDiffLines(t *testing.T) {
	text := "+line1\n-line2\n context\n+line4\n-line5"

	displayed, total := FormatDiffLines(text, 3)
	if total != 5 {
		t.Errorf("expected total 5, got %d", total)
	}
	if len(displayed) != 3 {
		t.Errorf("expected 3 displayed lines, got %d", len(displayed))
	}
	if displayed[0] != "+line1" {
		t.Errorf("expected first line +line1, got %q", displayed[0])
	}

	// No limit
	displayed, total = FormatDiffLines(text, 0)
	if total != 5 {
		t.Errorf("expected total 5, got %d", total)
	}
	if len(displayed) != 5 {
		t.Errorf("expected 5 displayed lines, got %d", len(displayed))
	}

	// Fewer lines than max
	displayed, total = FormatDiffLines(text, 100)
	if total != 5 {
		t.Errorf("expected total 5, got %d", total)
	}
	if len(displayed) != 5 {
		t.Errorf("expected all 5 lines, got %d", len(displayed))
	}
}

func TestFormatDiffLinesTrailingNewline(t *testing.T) {
	text := "+added\n-removed\n"
	displayed, total := FormatDiffLines(text, 0)
	if total != 2 {
		t.Errorf("expected 2 lines (trailing newline stripped), got %d", total)
	}
	if len(displayed) != 2 {
		t.Errorf("expected 2 displayed, got %d", len(displayed))
	}
}

func TestParseEditToolUseStoresFullInput(t *testing.T) {
	raw := `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"tool_use","name":"Edit","id":"toolu_edit1","input":{"file_path":"main.go","old_string":"foo","new_string":"bar"}}]}}`
	e := ParseLine(raw)
	if e.Kind != KindToolUse {
		t.Fatalf("expected KindToolUse, got %d", e.Kind)
	}
	if e.ToolName != "Edit" {
		t.Fatalf("expected Edit, got %q", e.ToolName)
	}
	if e.ToolInputFull == "" {
		t.Fatal("expected ToolInputFull to be populated for Edit calls")
	}
	if !strings.Contains(e.ToolInputFull, "old_string") {
		t.Errorf("ToolInputFull should contain old_string, got %q", e.ToolInputFull)
	}
	if !strings.Contains(e.ToolInputFull, "new_string") {
		t.Errorf("ToolInputFull should contain new_string, got %q", e.ToolInputFull)
	}
}

func TestParseNonEditToolUseNoFullInput(t *testing.T) {
	raw := `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"tool_use","name":"Read","id":"toolu_r1","input":{"file_path":"main.go"}}]}}`
	e := ParseLine(raw)
	if e.ToolInputFull != "" {
		t.Errorf("ToolInputFull should be empty for non-Edit tools, got %q", e.ToolInputFull)
	}
}

func TestParseToolResultDiffStoresFullContent(t *testing.T) {
	raw := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_edit1","type":"tool_result","content":"+added line\n-removed line\n context"}]}}`
	e := ParseLine(raw)
	if e.Kind != KindToolResult {
		t.Fatalf("expected KindToolResult, got %d", e.Kind)
	}
	if e.ToolResultFull == "" {
		t.Fatal("expected ToolResultFull for diff content")
	}
	if !strings.Contains(e.ToolResultFull, "+added") {
		t.Errorf("ToolResultFull should contain diff, got %q", e.ToolResultFull)
	}
}

func TestParseToolResultNonDiffNoFullContent(t *testing.T) {
	raw := `{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_r1","type":"tool_result","content":"some normal result"}]}}`
	e := ParseLine(raw)
	if e.ToolResultFull != "" {
		t.Errorf("ToolResultFull should be empty for non-diff content, got %q", e.ToolResultFull)
	}
}

func TestParseToolCallFingerprint_ToolUse(t *testing.T) {
	raw := `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"tool_use","name":"Read","id":"toolu_123","input":{"file_path":"internal/tui/model.go"}}]}}`
	fp := ParseToolCallFingerprint(raw)
	if !fp.IsToolCall {
		t.Fatal("expected IsToolCall=true for tool_use entry")
	}
	if fp.ToolName != "Read" {
		t.Fatalf("expected ToolName=Read, got %q", fp.ToolName)
	}
	if fp.InputHash == 0 {
		t.Fatal("expected non-zero InputHash")
	}
}

func TestParseToolCallFingerprint_NonToolUse(t *testing.T) {
	raw := `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"text","text":"thinking..."}]}}`
	fp := ParseToolCallFingerprint(raw)
	if fp.IsToolCall {
		t.Fatal("expected IsToolCall=false for text entry")
	}
	if fp.ToolName != "" {
		t.Fatalf("expected empty ToolName, got %q", fp.ToolName)
	}
	if fp.InputHash != 0 {
		t.Fatal("expected zero InputHash for non-tool entry")
	}
}

func TestParseToolCallFingerprint_SameInputSameHash(t *testing.T) {
	raw := `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"tool_use","name":"Read","id":"toolu_1","input":{"file_path":"foo.go"}}]}}`
	fp1 := ParseToolCallFingerprint(raw)
	fp2 := ParseToolCallFingerprint(raw)
	if fp1.InputHash != fp2.InputHash {
		t.Fatalf("same input should produce same hash: %d != %d", fp1.InputHash, fp2.InputHash)
	}
}

func TestParseToolCallFingerprint_DiffInputDiffHash(t *testing.T) {
	raw1 := `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"tool_use","name":"Read","id":"toolu_1","input":{"file_path":"foo.go"}}]}}`
	raw2 := `{"type":"assistant","message":{"type":"message","role":"assistant","content":[{"type":"tool_use","name":"Read","id":"toolu_2","input":{"file_path":"bar.go"}}]}}`
	fp1 := ParseToolCallFingerprint(raw1)
	fp2 := ParseToolCallFingerprint(raw2)
	if fp1.InputHash == fp2.InputHash {
		t.Fatal("different inputs should produce different hashes")
	}
}
