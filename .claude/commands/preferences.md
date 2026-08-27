---
description: "View and manage user preferences. Shows current preferences from global and project files."
argument-hint: "[show|edit|path]"
allowed-tools:
  - Read
  - Edit
---

# Preferences Manager

Display and manage user preference files used for session context injection (B-258).

## Preference Files

| Scope | Path | Purpose |
|-------|------|---------|
| Global | `~/.claude/PREFERENCES.md` | Personal preferences applied across all projects |
| Project | `.claude/preferences.md` | Project-specific preferences (optional) |

## Subcommands

Based on `$ARGUMENTS`:

- **`show`** (default, or empty): Read and display both preference files
- **`edit`**: Open the global preference file for editing with the Edit tool
- **`project edit`**: Open the project preference file for editing (create if needed)
- **`path`**: Show file paths without reading contents

## Action

Read and display the global preferences file:

```
~/.claude/PREFERENCES.md
```

If a project-level file exists, also read:

```
.claude/preferences.md
```

Display the contents clearly, noting which file each preference comes from. If the user wants to edit preferences, use the Edit tool on the appropriate file.

For `path` subcommand, just list the file paths and whether they exist.
