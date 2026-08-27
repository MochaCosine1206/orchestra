package onboard

import (
	"testing"

	"github.com/MochaCosine1206/orchestra/internal/scaffold"
)

func TestResultToScaffoldOptions(t *testing.T) {
	r := &Result{
		ProjectName: "my-project",
		ProjectType: scaffold.ProjectGo,
		WriteClaude: true,
		WriteMCP:    true,
		WriteLoops:  false,
	}

	opts := r.ToScaffoldOptions("/path/to/project")

	if opts.ProjectRoot != "/path/to/project" {
		t.Errorf("expected ProjectRoot '/path/to/project', got %q", opts.ProjectRoot)
	}
	if opts.ProjectName != "my-project" {
		t.Errorf("expected ProjectName 'my-project', got %q", opts.ProjectName)
	}
	if opts.ProjectType != scaffold.ProjectGo {
		t.Errorf("expected ProjectType 'go', got %q", opts.ProjectType)
	}
	if !opts.WriteClaude {
		t.Error("expected WriteClaude to be true")
	}
	if !opts.WriteMCP {
		t.Error("expected WriteMCP to be true")
	}
	if opts.WriteLoops {
		t.Error("expected WriteLoops to be false")
	}
}

func TestResultDefaults(t *testing.T) {
	r := &Result{
		ProjectName: "test",
		ProjectType: scaffold.ProjectTypeScript,
		WriteClaude: true,
		WriteMCP:    true,
		WriteLoops:  true,
	}

	opts := r.ToScaffoldOptions("/root")
	if opts.Force {
		t.Error("expected Force to default to false")
	}
}

func TestResultProjectTypes(t *testing.T) {
	types := []scaffold.ProjectType{
		scaffold.ProjectGo,
		scaffold.ProjectTypeScript,
		scaffold.ProjectPython,
		scaffold.ProjectRust,
		scaffold.ProjectOther,
	}

	for _, pt := range types {
		r := &Result{
			ProjectName: "test",
			ProjectType: pt,
		}
		opts := r.ToScaffoldOptions("/root")
		if opts.ProjectType != pt {
			t.Errorf("expected ProjectType %q, got %q", pt, opts.ProjectType)
		}
	}
}
