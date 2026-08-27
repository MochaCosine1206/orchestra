package panels

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/MochaCosine1206/orchestra/internal/orchestrator"
)

// ClarifySubmitMsg is sent when the user finishes answering clarification questions.
type ClarifySubmitMsg struct {
	Questions []orchestrator.ClarifyQuestion
}

// ClarifySkipMsg is sent when the user skips/cancels clarification (use defaults).
type ClarifySkipMsg struct{}

// ClarifyPanel displays clarification questions with selectable options.
// Each question shows numbered options the user can pick with arrow keys + Enter,
// or they can press 'o' to type a custom answer.
type ClarifyPanel struct {
	questions []orchestrator.ClarifyQuestion
	answers   []string // selected answer per question
	current   int      // current question index
	option    int      // selected option index within current question (0-based; last = "Other")
	typing    bool     // true when user is typing a custom answer
	input     textinput.Model
	width     int
	height    int
	focused   bool
}

// NewClarifyPanel creates a new empty clarify panel.
func NewClarifyPanel() *ClarifyPanel {
	ti := textinput.New()
	ti.Prompt = "→ "
	ti.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#06B6D4"))
	ti.CharLimit = 200
	return &ClarifyPanel{input: ti}
}

// SetQuestions populates the panel with questions.
func (p *ClarifyPanel) SetQuestions(questions []orchestrator.ClarifyQuestion) {
	p.questions = questions
	p.answers = make([]string, len(questions))
	p.current = 0
	p.option = 0
	p.typing = false

	// Pre-select the default (first option) for each question
	for i, q := range questions {
		if len(q.Options) > 0 {
			p.answers[i] = q.Options[0]
		} else {
			p.answers[i] = q.Default
		}
	}
}

// SetSize sets the panel dimensions.
func (p *ClarifyPanel) SetSize(w, h int) {
	p.width = w
	p.height = h
	p.input.Width = w - 12
}

// Focus activates the panel.
func (p *ClarifyPanel) Focus() { p.focused = true }

// Blur deactivates the panel.
func (p *ClarifyPanel) Blur() { p.focused = false }

// Active returns true if the panel has questions to display.
func (p *ClarifyPanel) Active() bool { return len(p.questions) > 0 }

// optionCount returns total selectable items for current question (options + "Other").
func (p *ClarifyPanel) optionCount() int {
	if p.current >= len(p.questions) {
		return 1
	}
	n := len(p.questions[p.current].Options)
	if n == 0 {
		return 1 // just the text input
	}
	return n + 1 // options + "Other"
}

// Update handles key messages. Returns a tea.Cmd.
func (p *ClarifyPanel) Update(msg tea.Msg) tea.Cmd {
	if !p.focused || len(p.questions) == 0 {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// If typing custom answer, delegate to text input
		if p.typing {
			switch msg.Type {
			case tea.KeyEnter:
				// Accept custom answer and advance
				val := strings.TrimSpace(p.input.Value())
				if val != "" {
					p.answers[p.current] = val
				}
				p.typing = false
				p.input.Blur()
				return p.advance()
			case tea.KeyEscape:
				// Cancel custom input, go back to option selection
				p.typing = false
				p.input.Blur()
				return nil
			default:
				var cmd tea.Cmd
				p.input, cmd = p.input.Update(msg)
				return cmd
			}
		}

		// Option selection mode
		switch msg.Type {
		case tea.KeyUp, tea.KeyShiftTab:
			if p.option > 0 {
				p.option--
			}
			return nil

		case tea.KeyDown, tea.KeyTab:
			if p.option < p.optionCount()-1 {
				p.option++
			}
			return nil

		case tea.KeyEnter:
			// If on "Other", start typing
			opts := p.questions[p.current].Options
			if len(opts) > 0 && p.option == len(opts) {
				p.typing = true
				p.input.SetValue("")
				p.input.Focus()
				return nil
			}
			// Select the current option
			if p.option < len(opts) {
				p.answers[p.current] = opts[p.option]
			}
			return p.advance()

		case tea.KeyEscape:
			return func() tea.Msg { return ClarifySkipMsg{} }
		}
	}

	return nil
}

// advance moves to next question or submits if on the last one.
func (p *ClarifyPanel) advance() tea.Cmd {
	if p.current < len(p.questions)-1 {
		p.current++
		p.option = 0
		return nil
	}
	return p.submit()
}

// submit builds answered questions and returns a ClarifySubmitMsg.
func (p *ClarifyPanel) submit() tea.Cmd {
	answered := make([]orchestrator.ClarifyQuestion, len(p.questions))
	copy(answered, p.questions)
	for i := range answered {
		if p.answers[i] != "" {
			answered[i].Answer = p.answers[i]
		} else {
			answered[i].Answer = answered[i].Default
		}
	}
	return func() tea.Msg { return ClarifySubmitMsg{Questions: answered} }
}

// View renders the clarify panel.
func (p *ClarifyPanel) View() string {
	if len(p.questions) == 0 {
		return ""
	}

	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#F59E0B")).
		MarginBottom(1)

	questionStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#E5E7EB"))

	contextStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#9CA3AF")).
		Italic(true)

	selectedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#06B6D4")).
		Bold(true)

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#6B7280"))

	wrapWidth := p.width - 6
	if wrapWidth < 30 {
		wrapWidth = 30
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render(fmt.Sprintf("  Goal Clarification (%d questions)", len(p.questions))))
	b.WriteString("\n")

	for i, q := range p.questions {
		isCurrent := i == p.current

		// Question header
		qText := fmt.Sprintf("[%d/%d] %s", i+1, len(p.questions), q.Question)
		if isCurrent {
			b.WriteString(fmt.Sprintf("  %s\n", questionStyle.Render(qText)))
		} else {
			b.WriteString(fmt.Sprintf("  %s\n", dimStyle.Render(qText)))
		}

		// Context (wrapped)
		if q.Context != "" && isCurrent {
			for _, line := range wrapText(q.Context, wrapWidth) {
				b.WriteString(fmt.Sprintf("    %s\n", contextStyle.Render(line)))
			}
		}

		if isCurrent {
			// Show options for current question
			for j, opt := range q.Options {
				if j == p.option && !p.typing {
					b.WriteString(fmt.Sprintf("    %s\n", selectedStyle.Render(fmt.Sprintf("▶ %s", opt))))
				} else {
					b.WriteString(fmt.Sprintf("      %s\n", dimStyle.Render(opt)))
				}
			}
			// "Other" option
			if len(q.Options) > 0 {
				otherIdx := len(q.Options)
				if p.option == otherIdx && !p.typing {
					b.WriteString(fmt.Sprintf("    %s\n", selectedStyle.Render("▶ Other (type custom)")))
				} else if p.typing {
					b.WriteString(fmt.Sprintf("    %s\n", selectedStyle.Render("  Other:")))
					b.WriteString(fmt.Sprintf("    %s\n", p.input.View()))
				} else {
					b.WriteString(fmt.Sprintf("      %s\n", dimStyle.Render("Other (type custom)")))
				}
			} else {
				// No options — show text input with default pre-filled
				if !p.typing {
					b.WriteString(fmt.Sprintf("    %s\n", dimStyle.Render(fmt.Sprintf("default: %s (Enter to accept, or type)", q.Default))))
				}
			}
		} else {
			// Show selected answer for completed questions
			if p.answers[i] != "" {
				b.WriteString(fmt.Sprintf("    %s\n", selectedStyle.Render(fmt.Sprintf("✓ %s", p.answers[i]))))
			}
		}
		b.WriteString("\n")
	}

	footerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#6B7280"))
	b.WriteString(footerStyle.Render("  ↑/↓: select  Enter: confirm  Esc: skip and use recommended defaults"))

	return b.String()
}

// wrapText is defined in log.go — reused here for context wrapping.
