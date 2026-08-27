package healing

import (
	"regexp"
	"strings"
)

// ErrorType classifies a build error.
type ErrorType string

const (
	ErrorMissingImport ErrorType = "missing_import"
	ErrorMissingDep    ErrorType = "missing_dep"
	ErrorSyntax        ErrorType = "syntax_error"
	ErrorTypeError     ErrorType = "type_error"
	ErrorUnknown       ErrorType = "unknown"
)

// BuildError represents a single parsed build error.
type BuildError struct {
	Type    ErrorType
	Message string
	Line    int
	File    string
}

var (
	reMissingImport = regexp.MustCompile(`could not import`)
	reMissingDep    = regexp.MustCompile(`cannot find module|missing go\.sum entry`)
	reSyntax        = regexp.MustCompile(`syntax error`)
	reTypeError     = regexp.MustCompile(`cannot use .* as .* in|cannot convert|have .* want .*`)
)

// ParseBuildError scans stderr line-by-line and returns classified BuildErrors.
func ParseBuildError(stderr string) []BuildError {
	var errors []BuildError
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var errType ErrorType
		switch {
		case reMissingImport.MatchString(line):
			errType = ErrorMissingImport
		case reMissingDep.MatchString(line):
			errType = ErrorMissingDep
		case reSyntax.MatchString(line):
			errType = ErrorSyntax
		case reTypeError.MatchString(line):
			errType = ErrorTypeError
		default:
			continue
		}
		errors = append(errors, BuildError{
			Type:    errType,
			Message: line,
		})
	}
	return errors
}
