package bridge

import "encoding/json"

// --- Hook Input Types (from Claude Code) ---

// StopHookInput is the JSON body Claude Code sends to the Stop hook.
type StopHookInput struct {
	SessionID            string `json:"session_id"`
	LastAssistantMessage string `json:"last_assistant_message"`
	Cwd                  string `json:"cwd"`
}

// PostToolUseInput is the JSON body for PostToolUse hooks.
type PostToolUseInput struct {
	SessionID    string          `json:"session_id"`
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	Cwd          string          `json:"cwd"`
}

// PreToolUseInput is the JSON body for PreToolUse hooks.
type PreToolUseInput struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	Cwd       string          `json:"cwd"`
}

// PermissionInput is the JSON body for PermissionRequest hooks.
type PermissionInput struct {
	SessionID string          `json:"session_id"`
	ToolName  string          `json:"tool_name"`
	ToolInput json.RawMessage `json:"tool_input"`
	Cwd       string          `json:"cwd"`
}

// BashToolInput extracts the command from Bash tool_input.
type BashToolInput struct {
	Command string `json:"command"`
}

// EditToolInput extracts file_path from Edit tool_input.
type EditToolInput struct {
	FilePath string `json:"file_path"`
}

// WriteToolInput extracts file_path from Write tool_input.
type WriteToolInput struct {
	FilePath string `json:"file_path"`
}

// AskUserQuestion extracts questions from AskUserQuestion tool_input.
type AskUserQuestionInput struct {
	Questions []AskUserQ `json:"questions"`
}

// AskUserQ is a single question in AskUserQuestion.
type AskUserQ struct {
	Question    string         `json:"question"`
	Header      string         `json:"header"`
	MultiSelect bool           `json:"multiSelect"`
	Options     []AskUserOpt   `json:"options"`
}

// AskUserOpt is an option for AskUserQuestion.
type AskUserOpt struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// --- Hook Output Types (responses back to Claude Code) ---

// HookResponse is the generic response sent back to Claude Code.
type HookResponse struct {
	Decision           string              `json:"decision,omitempty"`
	Reason             string              `json:"reason,omitempty"`
	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// HookSpecificOutput wraps event-specific output.
type HookSpecificOutput struct {
	HookEventName string           `json:"hookEventName,omitempty"`
	Decision      *PermissionDecision `json:"decision,omitempty"`
}

// PermissionDecision is the decision for PermissionRequest hooks.
type PermissionDecision struct {
	Behavior     string        `json:"behavior"`
	UpdatedInput *UpdatedInput `json:"updatedInput,omitempty"`
}

// UpdatedInput carries modified tool input back (used for AskUserQuestion answers).
type UpdatedInput struct {
	Questions []AskUserQ        `json:"questions,omitempty"`
	Answers   map[string]string `json:"answers,omitempty"`
}
