// Package onboard provides an interactive questionnaire for orchestra init.
// It uses charmbracelet/huh forms to guide users through project setup.
package onboard

import (
	"github.com/charmbracelet/huh"

	"github.com/MochaCosine1206/orchestra/internal/scaffold"
)

// Result holds the answers from the interactive onboarding questionnaire.
type Result struct {
	ProjectName       string
	ProjectType       scaffold.ProjectType
	WriteClaude       bool
	WriteMCP          bool
	WriteLoops        bool
	DefaultProjectDir string
}

// ToScaffoldOptions converts onboarding results to scaffold options.
func (r *Result) ToScaffoldOptions(projectRoot string) scaffold.Options {
	return scaffold.Options{
		ProjectRoot: projectRoot,
		ProjectName: r.ProjectName,
		ProjectType: r.ProjectType,
		WriteClaude: r.WriteClaude,
		WriteMCP:    r.WriteMCP,
		WriteLoops:  r.WriteLoops,
	}
}

// Run launches the interactive onboarding questionnaire.
// It auto-detects project type and pre-fills defaults.
// existingProjectDir pre-fills the default project directory input (from global config).
func Run(projectRoot, defaultName string, detectedType scaffold.ProjectType, existingProjectDir string) (*Result, error) {
	result := &Result{
		ProjectName:       defaultName,
		ProjectType:       detectedType,
		WriteClaude:       true,
		WriteMCP:          true,
		WriteLoops:        true,
		DefaultProjectDir: existingProjectDir,
	}

	projectTypeStr := string(detectedType)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Welcome to Claude Orchestra!").
				Description("Let's set up your project for multi-agent orchestration."),

			huh.NewInput().
				Title("Project name").
				Value(&result.ProjectName),

			huh.NewSelect[string]().
				Title("What type of project?").
				Options(
					huh.NewOption("Go", "go"),
					huh.NewOption("TypeScript", "typescript"),
					huh.NewOption("Python", "python"),
					huh.NewOption("Rust", "rust"),
					huh.NewOption("Other", "other"),
				).
				Value(&projectTypeStr),

			huh.NewInput().
				Title("Default directory for new projects").
				Description("Where should 'orchestra new' create projects? (leave blank to skip)").
				Value(&result.DefaultProjectDir),

			huh.NewConfirm().
				Title("Enable Claude Code integration?").
				Description("Scaffolds .orchestra/agents/, .orchestra/profiles/, .claude/commands/").
				Value(&result.WriteClaude),

			huh.NewConfirm().
				Title("Install MCP servers?").
				Description("Installs sqlite, memory, git-worktree, pm, filesystem, playwright via claude mcp add").
				Value(&result.WriteMCP),

			huh.NewConfirm().
				Title("Create loop scaffolding?").
				Description("Creates .orchestra/ dirs for research, specs, ideas, creativity output").
				Value(&result.WriteLoops),
		),
	)

	if err := form.Run(); err != nil {
		return nil, err
	}

	result.ProjectType = scaffold.ProjectType(projectTypeStr)

	return result, nil
}
