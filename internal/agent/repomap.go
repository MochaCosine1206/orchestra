package agent

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// skipDirs are directories excluded from repo map generation.
var skipDirs = map[string]bool{
	".git":         true,
	"vendor":       true,
	"node_modules": true,
	".worktree":    true,
	".orchestra":   true,
	"__pycache__":  true,
	".venv":        true,
}

// GenerateRepoMap creates a compact codebase summary within a token budget.
// For Go files: uses go/parser for accurate AST-based extraction.
// For other files: uses regex-based signature detection.
func GenerateRepoMap(repoRoot string, tokenBudget int) (string, error) {
	if repoRoot == "" {
		return "", fmt.Errorf("repoRoot is required")
	}

	maxChars := tokenBudget * CharsPerToken
	var entries []repoMapEntry

	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip errors
		}

		// Skip excluded directories
		if info.IsDir() {
			name := info.Name()
			if skipDirs[name] || strings.HasPrefix(name, ".") {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary and large files
		if info.Size() > 500_000 || info.Size() == 0 {
			return nil
		}

		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return nil
		}

		ext := filepath.Ext(path)
		var sigs []string

		switch ext {
		case ".go":
			sigs = extractGoSignatures(path)
		case ".py":
			sigs = extractRegexSignatures(path, pythonPatterns)
		case ".js", ".ts", ".jsx", ".tsx":
			sigs = extractRegexSignatures(path, jsPatterns)
		case ".sql":
			sigs = extractRegexSignatures(path, sqlPatterns)
		case ".md":
			sigs = extractRegexSignatures(path, markdownPatterns)
		default:
			return nil // skip unknown file types
		}

		if len(sigs) > 0 {
			entries = append(entries, repoMapEntry{
				path:    relPath,
				sigs:    sigs,
				modTime: info.ModTime().Unix(),
			})
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walking repo: %w", err)
	}

	// Sort: recently modified first
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime > entries[j].modTime
	})

	// Build output within budget
	var b strings.Builder
	totalChars := 0

	for _, e := range entries {
		line := fmt.Sprintf("%s: %s\n", e.path, strings.Join(e.sigs, " | "))
		if totalChars+len(line) > maxChars {
			break
		}
		b.WriteString(line)
		totalChars += len(line)
	}

	return b.String(), nil
}

type repoMapEntry struct {
	path    string
	sigs    []string
	modTime int64
}

// extractGoSignatures uses go/parser to extract function, type, and interface declarations.
func extractGoSignatures(path string) []string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		return nil
	}

	var sigs []string

	// Package name
	sigs = append(sigs, "pkg "+f.Name.Name)

	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			sig := formatFuncSig(d)
			if sig != "" {
				sigs = append(sigs, sig)
			}

		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					sig := formatTypeSig(s)
					if sig != "" {
						sigs = append(sigs, sig)
					}
				}
			}
		}
	}

	return sigs
}

// formatFuncSig formats a function declaration into a compact signature.
func formatFuncSig(f *ast.FuncDecl) string {
	if f.Name == nil || !f.Name.IsExported() {
		return ""
	}

	var b strings.Builder
	b.WriteString("func ")

	// Receiver
	if f.Recv != nil && len(f.Recv.List) > 0 {
		recvType := exprString(f.Recv.List[0].Type)
		b.WriteString("(" + recvType + ") ")
	}

	b.WriteString(f.Name.Name)
	b.WriteString("(")

	// Parameters (types only)
	if f.Type.Params != nil {
		var params []string
		for _, p := range f.Type.Params.List {
			typeStr := exprString(p.Type)
			params = append(params, typeStr)
		}
		b.WriteString(strings.Join(params, ", "))
	}
	b.WriteString(")")

	// Return types
	if f.Type.Results != nil && len(f.Type.Results.List) > 0 {
		var results []string
		for _, r := range f.Type.Results.List {
			results = append(results, exprString(r.Type))
		}
		if len(results) == 1 {
			b.WriteString(" " + results[0])
		} else {
			b.WriteString(" (" + strings.Join(results, ", ") + ")")
		}
	}

	return b.String()
}

// formatTypeSig formats a type declaration into a compact signature.
func formatTypeSig(s *ast.TypeSpec) string {
	if s.Name == nil || !s.Name.IsExported() {
		return ""
	}

	switch t := s.Type.(type) {
	case *ast.StructType:
		var fields []string
		if t.Fields != nil {
			for _, f := range t.Fields.List {
				if len(f.Names) > 0 && f.Names[0].IsExported() {
					fields = append(fields, f.Names[0].Name+" "+exprString(f.Type))
				}
			}
		}
		if len(fields) > 0 {
			return fmt.Sprintf("type %s struct { %s }", s.Name.Name, strings.Join(fields, "; "))
		}
		return "type " + s.Name.Name + " struct"

	case *ast.InterfaceType:
		var methods []string
		if t.Methods != nil {
			for _, m := range t.Methods.List {
				if len(m.Names) > 0 {
					methods = append(methods, m.Names[0].Name+"()")
				}
			}
		}
		if len(methods) > 0 {
			return fmt.Sprintf("type %s interface { %s }", s.Name.Name, strings.Join(methods, "; "))
		}
		return "type " + s.Name.Name + " interface"

	default:
		return "type " + s.Name.Name
	}
}

// exprString returns a simple string representation of a Go AST expression.
func exprString(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return exprString(e.X) + "." + e.Sel.Name
	case *ast.StarExpr:
		return "*" + exprString(e.X)
	case *ast.ArrayType:
		return "[]" + exprString(e.Elt)
	case *ast.MapType:
		return "map[" + exprString(e.Key) + "]" + exprString(e.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		return "func"
	case *ast.Ellipsis:
		return "..." + exprString(e.Elt)
	case *ast.ChanType:
		return "chan " + exprString(e.Value)
	default:
		return "?"
	}
}

// Regex patterns for non-Go languages.
var (
	pythonPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^(def |class |async def )\s*(\w+)`),
	}
	jsPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^(?:export\s+)?(?:function|class|const|interface)\s+(\w+)`),
	}
	sqlPatterns = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^CREATE\s+(TABLE|VIEW|INDEX)\s+(?:IF\s+NOT\s+EXISTS\s+)?(\w+)`),
	}
	markdownPatterns = []*regexp.Regexp{
		regexp.MustCompile(`^#{1,3}\s+(.+)`),
	}
)

// extractRegexSignatures extracts signatures from a file using regex patterns.
func extractRegexSignatures(path string, patterns []*regexp.Regexp) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	var sigs []string
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		for _, p := range patterns {
			if m := p.FindStringSubmatch(line); len(m) > 1 {
				// Use the last capture group as the signature
				sig := m[len(m)-1]
				if sig != "" {
					sigs = append(sigs, sig)
				}
				break
			}
		}
	}

	// Limit per file
	if len(sigs) > 20 {
		sigs = sigs[:20]
	}

	return sigs
}
