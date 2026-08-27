package orchestra

import (
	"regexp"
	"strings"
)

// RenderMarkdown converts a subset of Markdown to HTML without external dependencies.
// Supported elements: headers (h1-h6), bold, italic, fenced code blocks,
// unordered lists, ordered lists, and links.
func RenderMarkdown(input string) string {
	lines := strings.Split(input, "\n")
	var out []string
	i := 0

	for i < len(lines) {
		line := lines[i]

		// Fenced code blocks
		if strings.HasPrefix(line, "```") {
			lang := strings.TrimPrefix(line, "```")
			lang = strings.TrimSpace(lang)
			i++
			var code []string
			for i < len(lines) && !strings.HasPrefix(lines[i], "```") {
				code = append(code, escapeHTML(lines[i]))
				i++
			}
			if i < len(lines) {
				i++ // skip closing ```
			}
			if lang != "" {
				out = append(out, `<pre><code class="language-`+escapeHTML(lang)+`">`+strings.Join(code, "\n")+"</code></pre>")
			} else {
				out = append(out, "<pre><code>"+strings.Join(code, "\n")+"</code></pre>")
			}
			continue
		}

		// Headers
		if strings.HasPrefix(line, "#") {
			level := 0
			for level < len(line) && level < 6 && line[level] == '#' {
				level++
			}
			if level <= 6 && level < len(line) && line[level] == ' ' {
				text := renderInline(strings.TrimSpace(line[level+1:]))
				out = append(out, "<h"+itoa(level)+">"+text+"</h"+itoa(level)+">")
				i++
				continue
			}
		}

		// Unordered list
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			var items []string
			for i < len(lines) && (strings.HasPrefix(lines[i], "- ") || strings.HasPrefix(lines[i], "* ")) {
				items = append(items, "<li>"+renderInline(lines[i][2:])+"</li>")
				i++
			}
			out = append(out, "<ul>"+strings.Join(items, "")+"</ul>")
			continue
		}

		// Ordered list
		if isOrderedListItem(line) {
			var items []string
			for i < len(lines) && isOrderedListItem(lines[i]) {
				text := orderedListText(lines[i])
				items = append(items, "<li>"+renderInline(text)+"</li>")
				i++
			}
			out = append(out, "<ol>"+strings.Join(items, "")+"</ol>")
			continue
		}

		// Blank line
		if strings.TrimSpace(line) == "" {
			i++
			continue
		}

		// Paragraph
		out = append(out, "<p>"+renderInline(line)+"</p>")
		i++
	}

	return strings.Join(out, "\n")
}

// renderInline handles bold, italic, inline code, and links.
func renderInline(s string) string {
	s = escapeHTML(s)

	// Inline code (before bold/italic to avoid conflicts)
	s = regexp.MustCompile("`([^`]+)`").ReplaceAllString(s, "<code>$1</code>")

	// Bold (**text** or __text__)
	s = regexp.MustCompile(`\*\*(.+?)\*\*`).ReplaceAllString(s, "<strong>$1</strong>")
	s = regexp.MustCompile(`__(.+?)__`).ReplaceAllString(s, "<strong>$1</strong>")

	// Italic (*text* or _text_)
	s = regexp.MustCompile(`\*(.+?)\*`).ReplaceAllString(s, "<em>$1</em>")
	s = regexp.MustCompile(`_(.+?)_`).ReplaceAllString(s, "<em>$1</em>")

	// Links [text](url)
	s = regexp.MustCompile(`\[([^\]]+)\]\(([^)]+)\)`).ReplaceAllString(s, `<a href="$2">$1</a>`)

	return s
}

func escapeHTML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

func isOrderedListItem(line string) bool {
	for i, c := range line {
		if c >= '0' && c <= '9' {
			continue
		}
		if c == '.' && i > 0 && i+1 < len(line) && line[i+1] == ' ' {
			return true
		}
		return false
	}
	return false
}

func orderedListText(line string) string {
	idx := strings.Index(line, ". ")
	if idx < 0 {
		return line
	}
	return line[idx+2:]
}

func itoa(n int) string {
	return string(rune('0' + n))
}

// RenderMarkdownPreview wraps RenderMarkdown output in a styled container div
// suitable for inline artifact previews in the dashboard.
func RenderMarkdownPreview(input string) string {
	if input == "" {
		return ""
	}
	return `<div class="artifact-preview">` + RenderMarkdown(input) + `</div>`
}
