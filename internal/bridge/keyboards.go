package bridge

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// InlineKeyboard is the Telegram inline keyboard structure.
type InlineKeyboard struct {
	InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
}

// InlineButton is a single inline keyboard button.
type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data"`
}

// GenerateRequestID returns an 8-char hex ID for callback data.
func GenerateRequestID() string {
	b := make([]byte, 4)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ApprovalKeyboard builds [[Approve, Deny], [Skip]] keyboard JSON.
func ApprovalKeyboard(requestID string) string {
	kb := InlineKeyboard{
		InlineKeyboard: [][]InlineButton{
			{
				{Text: "\u2705 Approve", CallbackData: "approve:" + requestID},
				{Text: "\u274c Deny", CallbackData: "deny:" + requestID},
			},
			{
				{Text: "\u23ed Skip", CallbackData: "skip:" + requestID},
			},
		},
	}
	data, _ := json.Marshal(kb)
	return string(data)
}

// AskUserKeyboard builds a keyboard with one button per option + "Other".
func AskUserKeyboard(requestID string, questionIdx int, options []AskUserOpt) string {
	var rows [][]InlineButton
	for j, opt := range options {
		btnText := opt.Label
		if opt.Description != "" && len(opt.Description) <= 30 {
			btnText = opt.Label + " \u2014 " + opt.Description
		}
		cbData := fmt.Sprintf("aq:%s:%d:%d", requestID, questionIdx, j)
		rows = append(rows, []InlineButton{{Text: btnText, CallbackData: cbData}})
	}
	// Add "Other" button
	otherCB := fmt.Sprintf("aq:%s:%d:other", requestID, questionIdx)
	rows = append(rows, []InlineButton{{Text: "Other (type reply)", CallbackData: otherCB}})

	kb := InlineKeyboard{InlineKeyboard: rows}
	data, _ := json.Marshal(kb)
	return string(data)
}

// GitActionKeyboard returns the appropriate action keyboard JSON based on git state,
// or empty string if no action is available.
func GitActionKeyboard(dir, slug string) string {
	branch := gitBranch(dir)
	isTrunk := branch == "dev" || branch == "main" || branch == "master" || branch == ""
	hasChanges := gitHasChanges(dir)

	if isTrunk {
		if hasChanges {
			return actionKB("\U0001f4e4 Push It", "push:"+slug)
		}
		return ""
	}

	// Feature branch
	if hasChanges {
		return actionKB("\U0001f680 Ship It", "ship:"+slug)
	}

	prURL := ghPRURL(dir)
	if prURL != "" {
		return actionKB("\U0001f500 Merge", "merge:"+slug)
	}
	return actionKB("\U0001f4cb Create PR", "pr:"+slug)
}

func actionKB(text, cbData string) string {
	kb := InlineKeyboard{
		InlineKeyboard: [][]InlineButton{
			{{Text: text, CallbackData: cbData}},
		},
	}
	data, _ := json.Marshal(kb)
	return string(data)
}
