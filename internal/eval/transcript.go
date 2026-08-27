package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// TranscriptFunc returns a transcript string for a given scenario and version.
// Used by RunABTest to obtain transcripts — can be real (from logs) or synthetic.
type TranscriptFunc func(scenario Scenario, version string) string

// ReadTaskTranscript reads the JSONL log for a task and extracts assistant messages
// as a transcript suitable for judge evaluation.
func ReadTaskTranscript(logsDir, taskID string) (string, error) {
	logFile := filepath.Join(logsDir, taskID+".jsonl")
	data, err := os.ReadFile(logFile)
	if err != nil {
		return "", fmt.Errorf("reading transcript %s: %w", logFile, err)
	}

	var parts []string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var entry map[string]interface{}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}

		entryType, _ := entry["type"].(string)

		switch entryType {
		case "assistant":
			// Extract assistant message content
			if msg, ok := entry["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].([]interface{}); ok {
					for _, c := range content {
						if block, ok := c.(map[string]interface{}); ok {
							switch block["type"] {
							case "text":
								if text, ok := block["text"].(string); ok {
									parts = append(parts, "[assistant] "+text)
								}
							case "tool_use":
								name, _ := block["name"].(string)
								parts = append(parts, fmt.Sprintf("[tool_use] %s", name))
							}
						}
					}
				}
			}

		case "user":
			// Extract tool results (summarized)
			if msg, ok := entry["message"].(map[string]interface{}); ok {
				if content, ok := msg["content"].([]interface{}); ok {
					for _, c := range content {
						if block, ok := c.(map[string]interface{}); ok {
							if block["type"] == "tool_result" {
								result, _ := block["content"].(string)
								if len(result) > 200 {
									result = result[:200] + "..."
								}
								parts = append(parts, "[tool_result] "+result)
							}
						}
					}
				}
			}

		case "result":
			// Include the final result
			subtype, _ := entry["subtype"].(string)
			isError, _ := entry["is_error"].(bool)
			parts = append(parts, fmt.Sprintf("[result] subtype=%s is_error=%v", subtype, isError))
		}
	}

	if len(parts) == 0 {
		return "", fmt.Errorf("no transcript content found in %s", logFile)
	}

	return strings.Join(parts, "\n"), nil
}

// SyntheticTranscript returns a placeholder transcript for scenarios without real logs.
func SyntheticTranscript(scenario Scenario, version string) string {
	return fmt.Sprintf(
		"[%s] Executing scenario: %s\nRole: %s\nGoal: %s\nExpected: %s\nStatus: completed with partial success.",
		version, scenario.ID, scenario.Role, scenario.Goal, scenario.ExpectedOutcome,
	)
}
