package panels

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// GoalSubmitMsg is sent when the user presses Enter in the input panel.
type GoalSubmitMsg struct {
	Goal string
}

// InputCancelMsg is sent when the user presses Escape in the input panel.
type InputCancelMsg struct{}

const (
	maxVisibleItems = 8  // visible rows in popup window
	maxScanDepth    = 4  // directory depth for file scan
	maxTotalItems   = 200 // cap total candidates to keep things snappy
)

// junk files to exclude from autocomplete
var junkFiles = map[string]bool{
	".DS_Store": true, "Thumbs.db": true, ".gitkeep": true,
}

// InputPanel wraps a textinput.Model for goal entry with @file autocomplete.
type InputPanel struct {
	input    textinput.Model
	width    int
	focused  bool
	repoRoot string

	// Autocomplete state
	acActive    bool     // autocomplete popup visible
	acPrefix    string   // text after @ being matched
	acAtPos     int      // cursor position where @ was typed
	acItems     []string // all filtered candidates
	acSelected  int      // highlighted index (in full list)
	acOffset    int      // scroll offset (first visible index)
	acFileCache []string // cached list of repo files (relative paths)
}

// NewInputPanel creates a new input panel.
func NewInputPanel() *InputPanel {
	ti := textinput.New()
	ti.Prompt = "goal> "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#06B6D4")).Bold(true)
	ti.CharLimit = 500
	return &InputPanel{input: ti}
}

// SetRepoRoot sets the repo root for @file autocomplete.
func (p *InputPanel) SetRepoRoot(root string) {
	p.repoRoot = root
	p.acFileCache = nil // invalidate cache
}

// SetWidth sets the input width.
func (p *InputPanel) SetWidth(w int) {
	p.width = w
	p.input.Width = w - 8 // account for prompt
}

// Focus activates the input for typing.
func (p *InputPanel) Focus() {
	p.focused = true
	p.input.Focus()
	p.input.SetValue("")
	p.acActive = false
}

// Blur deactivates the input.
func (p *InputPanel) Blur() {
	p.focused = false
	p.input.Blur()
	p.acActive = false
}

// Focused returns whether the input is active.
func (p *InputPanel) Focused() bool {
	return p.focused
}

// Value returns the current input text.
func (p *InputPanel) Value() string {
	return p.input.Value()
}

// Update handles key messages for the input. Returns a tea.Cmd.
func (p *InputPanel) Update(msg tea.Msg) tea.Cmd {
	if !p.focused {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// When autocomplete is active, intercept navigation keys
		if p.acActive {
			switch msg.Type {
			case tea.KeyTab, tea.KeyRight:
				p.acceptCompletion()
				return nil
			case tea.KeyUp:
				if p.acSelected > 0 {
					p.acSelected--
					p.ensureVisible()
				}
				return nil
			case tea.KeyDown:
				if p.acSelected < len(p.acItems)-1 {
					p.acSelected++
					p.ensureVisible()
				}
				return nil
			case tea.KeyEscape:
				p.acActive = false
				return nil
			case tea.KeyEnter:
				p.acceptCompletion()
				return nil
			}
		}

		switch msg.Type {
		case tea.KeyEnter:
			val := p.input.Value()
			if val != "" {
				p.Blur()
				return func() tea.Msg { return GoalSubmitMsg{Goal: val} }
			}
			return nil
		case tea.KeyEscape:
			p.Blur()
			return func() tea.Msg { return InputCancelMsg{} }
		}
	}

	// Let textinput handle the keystroke
	var cmd tea.Cmd
	p.input, cmd = p.input.Update(msg)

	// After text changes, check for @file trigger
	p.updateAutocomplete()

	return cmd
}

// ensureVisible adjusts acOffset so acSelected is within the visible window.
func (p *InputPanel) ensureVisible() {
	if p.acSelected < p.acOffset {
		p.acOffset = p.acSelected
	}
	if p.acSelected >= p.acOffset+maxVisibleItems {
		p.acOffset = p.acSelected - maxVisibleItems + 1
	}
}

// updateAutocomplete checks if we're in an @file context and updates candidates.
func (p *InputPanel) updateAutocomplete() {
	val := p.input.Value()
	pos := p.input.Position()

	// Find the last @ before cursor that starts a file reference
	atIdx := -1
	for i := pos - 1; i >= 0; i-- {
		if val[i] == '@' {
			// Check it's not part of an email (preceded by alphanum)
			if i > 0 {
				prev := val[i-1]
				if (prev >= 'a' && prev <= 'z') || (prev >= 'A' && prev <= 'Z') || (prev >= '0' && prev <= '9') {
					break
				}
			}
			atIdx = i
			break
		}
		// Stop scanning if we hit a space before finding @
		if val[i] == ' ' {
			break
		}
	}

	if atIdx < 0 {
		p.acActive = false
		return
	}

	prefix := val[atIdx+1 : pos]
	p.acActive = true
	p.acAtPos = atIdx
	p.acPrefix = prefix
	p.acItems = p.filterFiles(prefix)
	p.acSelected = 0
	p.acOffset = 0
}

// acceptCompletion inserts the selected file into the input text.
func (p *InputPanel) acceptCompletion() {
	if !p.acActive || len(p.acItems) == 0 {
		p.acActive = false
		return
	}

	selected := p.acItems[p.acSelected]
	val := p.input.Value()

	// Replace @prefix with @selected
	before := val[:p.acAtPos+1] // includes the @
	after := ""
	endOfRef := p.acAtPos + 1 + len(p.acPrefix)
	if endOfRef < len(val) {
		after = val[endOfRef:]
	}

	newVal := before + selected + after
	p.input.SetValue(newVal)
	p.input.SetCursor(p.acAtPos + 1 + len(selected))
	p.acActive = false
}

// filterFiles returns repo files matching the given prefix (case-insensitive).
func (p *InputPanel) filterFiles(prefix string) []string {
	if p.repoRoot == "" {
		return nil
	}

	files := p.getFileCache()
	if prefix == "" {
		if len(files) > maxTotalItems {
			return files[:maxTotalItems]
		}
		return files
	}

	lower := strings.ToLower(prefix)
	var matches []string
	for _, f := range files {
		if strings.Contains(strings.ToLower(f), lower) {
			matches = append(matches, f)
			if len(matches) >= maxTotalItems {
				break
			}
		}
	}
	return matches
}

// getFileCache lazily scans the repo for files.
func (p *InputPanel) getFileCache() []string {
	if p.acFileCache != nil {
		return p.acFileCache
	}

	var files []string
	root := p.repoRoot

	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			return nil
		}

		// Skip hidden dirs and common non-useful dirs
		if d.IsDir() {
			name := d.Name()
			if strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			depth := strings.Count(rel, string(filepath.Separator))
			if depth >= maxScanDepth {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip junk files and hidden files
		name := d.Name()
		if junkFiles[name] || strings.HasPrefix(name, ".") {
			return nil
		}

		// Only include files with extensions (matches expandFileReferences pattern)
		if filepath.Ext(rel) != "" {
			files = append(files, rel)
		}
		return nil
	})

	sort.Strings(files)
	p.acFileCache = files
	return files
}

// PopupHeight returns the number of extra lines the autocomplete popup needs (0 if hidden).
func (p *InputPanel) PopupHeight() int {
	if !p.acActive || len(p.acItems) == 0 {
		return 0
	}
	visible := len(p.acItems)
	if visible > maxVisibleItems {
		visible = maxVisibleItems
	}
	return visible + 1 // visible items + hint line
}

// View renders the input panel with optional autocomplete popup above.
func (p *InputPanel) View() string {
	inputStyle := lipgloss.NewStyle().
		Width(p.width).
		Background(lipgloss.Color("#1F2937"))

	inputLine := inputStyle.Render(p.input.View())

	if !p.acActive || len(p.acItems) == 0 {
		return inputLine
	}

	popup := p.renderPopup()
	return popup + "\n" + inputLine
}

// renderPopup builds the scrollable autocomplete suggestions popup.
func (p *InputPanel) renderPopup() string {
	normal := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#D1D5DB")).
		Background(lipgloss.Color("#374151")).
		PaddingLeft(1).
		PaddingRight(1)

	selected := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#111827")).
		Background(lipgloss.Color("#06B6D4")).
		Bold(true).
		PaddingLeft(1).
		PaddingRight(1)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#6B7280")).
		Background(lipgloss.Color("#374151")).
		PaddingLeft(1).
		Italic(true)

	// Compute visible window
	end := p.acOffset + maxVisibleItems
	if end > len(p.acItems) {
		end = len(p.acItems)
	}
	visible := p.acItems[p.acOffset:end]

	var lines []string
	for i, item := range visible {
		globalIdx := p.acOffset + i
		style := normal
		if globalIdx == p.acSelected {
			style = selected
		}
		lines = append(lines, style.Width(p.width).Render("@"+item))
	}

	// Hint with scroll indicator
	total := len(p.acItems)
	hintText := "↑↓ navigate  Tab accept  Esc dismiss"
	if total > maxVisibleItems {
		hintText = fmt.Sprintf("%s  [%d/%d]", hintText, p.acSelected+1, total)
	}
	lines = append(lines, hint.Width(p.width).Render(hintText))

	return strings.Join(lines, "\n")
}
