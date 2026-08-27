# Task Specification: [TASK-ID]

## Overview

| Field | Value |
|---|---|
| **Task ID** | `task-XXX` |
| **Title** | [Short descriptive title] |
| **Assigned Role** | architect / implementer / reviewer / scout / researcher |
| **Model Tier** | opus / sonnet / haiku |
| **Priority** | 0 (low) — 10 (critical) |
| **Depends On** | [task IDs that must complete first, or "none"] |
| **Blocked By** | [task IDs currently blocking this, or "none"] |

## Description

[2-5 sentences describing what this task accomplishes and why it matters in the context of the larger feature.]

## Files Owned

These are the ONLY files this agent may create or modify:

| File Path | Action | Description |
|---|---|---|
| `src/example.ts` | create / modify | [What changes] |

## Inline Schema (MANDATORY)

**Use ONLY the definitions below. Do NOT guess or invent column names, field names, or API shapes. If information is missing, respond with `INSUFFICIENT_SCHEMA: [what's missing]` rather than guessing.**

Paste the exact definitions this task needs — SQL DDL, TypeScript interfaces, API contracts, etc.

## Interface Contracts

[If this task produces or consumes interfaces shared with other parallel tasks, define them here.]

If no shared interfaces: "None — this task is self-contained."

## Acceptance Criteria

- [ ] [Specific, testable criterion 1]
- [ ] [Specific, testable criterion 2]
- [ ] [Specific, testable criterion 3]
- [ ] All tests pass
- [ ] No files modified outside of ownership list

## Context

[Links to relevant specs, research, or existing code that the agent should read before starting.]

## Token Budget

| Limit | Value |
|---|---|
| **Max tokens** | [number or "unlimited"] |
| **Expected complexity** | low / medium / high |

## Notes

[Any additional context, warnings, or edge cases the agent should be aware of.]
