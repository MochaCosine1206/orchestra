package core

import (
	"strings"
	"testing"
)

func TestRenderMarkdown(t *testing.T) {
	t.Run("h1", func(t *testing.T) {
		got := RenderMarkdown("# Hello")
		if got != "<h1>Hello</h1>" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("h2", func(t *testing.T) {
		got := RenderMarkdown("## Subtitle")
		if got != "<h2>Subtitle</h2>" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("h3_through_h6", func(t *testing.T) {
		for i, hdr := range []string{"### H3", "#### H4", "##### H5", "###### H6"} {
			level := i + 3
			got := RenderMarkdown(hdr)
			expect := "<h" + string(rune('0'+level)) + ">"
			if !strings.Contains(got, expect) {
				t.Errorf("level %d: got %q, want %q prefix", level, got, expect)
			}
		}
	})

	t.Run("bold", func(t *testing.T) {
		got := RenderMarkdown("This is **bold** text")
		if !strings.Contains(got, "<strong>bold</strong>") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("italic", func(t *testing.T) {
		got := RenderMarkdown("This is *italic* text")
		if !strings.Contains(got, "<em>italic</em>") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("inline_code", func(t *testing.T) {
		got := RenderMarkdown("Use `fmt.Println` here")
		if !strings.Contains(got, "<code>fmt.Println</code>") {
			t.Errorf("got %q", got)
		}
	})

	t.Run("fenced_code_block", func(t *testing.T) {
		input := "```go\nfunc main() {}\n```"
		got := RenderMarkdown(input)
		if !strings.Contains(got, "<pre><code") {
			t.Errorf("missing pre/code: %q", got)
		}
		if !strings.Contains(got, "language-go") {
			t.Errorf("missing language class: %q", got)
		}
		if !strings.Contains(got, "func main()") {
			t.Errorf("missing code content: %q", got)
		}
	})

	t.Run("unordered_list", func(t *testing.T) {
		input := "- Item one\n- Item two\n- Item three"
		got := RenderMarkdown(input)
		if !strings.Contains(got, "<ul>") {
			t.Errorf("missing ul: %q", got)
		}
		if strings.Count(got, "<li>") != 3 {
			t.Errorf("expected 3 li, got %q", got)
		}
	})

	t.Run("ordered_list", func(t *testing.T) {
		input := "1. First\n2. Second\n3. Third"
		got := RenderMarkdown(input)
		if !strings.Contains(got, "<ol>") {
			t.Errorf("missing ol: %q", got)
		}
		if strings.Count(got, "<li>") != 3 {
			t.Errorf("expected 3 li, got %q", got)
		}
	})

	t.Run("links", func(t *testing.T) {
		got := RenderMarkdown("Visit [Google](https://google.com) now")
		if !strings.Contains(got, `<a href="https://google.com">Google</a>`) {
			t.Errorf("got %q", got)
		}
	})

	t.Run("paragraph", func(t *testing.T) {
		got := RenderMarkdown("Hello world")
		if got != "<p>Hello world</p>" {
			t.Errorf("got %q", got)
		}
	})

	t.Run("html_escaping", func(t *testing.T) {
		got := RenderMarkdown("Use <script> tags")
		if strings.Contains(got, "<script>") {
			t.Errorf("HTML not escaped: %q", got)
		}
		if !strings.Contains(got, "&lt;script&gt;") {
			t.Errorf("expected escaped HTML: %q", got)
		}
	})

	t.Run("mixed_content", func(t *testing.T) {
		input := "# Title\n\nSome **bold** text\n\n- item\n\n```\ncode\n```"
		got := RenderMarkdown(input)
		if !strings.Contains(got, "<h1>") {
			t.Errorf("missing h1: %q", got)
		}
		if !strings.Contains(got, "<strong>") {
			t.Errorf("missing strong: %q", got)
		}
		if !strings.Contains(got, "<ul>") {
			t.Errorf("missing ul: %q", got)
		}
		if !strings.Contains(got, "<pre>") {
			t.Errorf("missing pre: %q", got)
		}
	})
}
