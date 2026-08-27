package onboard

import (
	"context"
	"fmt"

	"github.com/charmbracelet/huh"

	"github.com/MochaCosine1206/orchestra/internal/bridge"
)

// Feature describes an optional Orchestra capability that can be enabled during init.
type Feature struct {
	Name         string
	Description  string
	RequiredDeps []string // Binary names from ScanResult that must be present
	Enabled      bool
	SetupFunc    func(ctx context.Context, projectRoot string) error
}

// DefaultFeatures returns the 6 standard features with their dependency requirements.
// SetupFunc is nil for features whose setup is handled by scaffold.Options flags.
func DefaultFeatures() []Feature {
	return []Feature{
		{
			Name:         "core",
			Description:  "Core orchestration (database, runtime dirs)",
			RequiredDeps: []string{"go", "git"},
			Enabled:      true, // always enabled
		},
		{
			Name:         "claude-integration",
			Description:  "Claude Code agents, profiles, and commands",
			RequiredDeps: []string{"claude"},
		},
		{
			Name:         "mcp-servers",
			Description:  "MCP servers (sqlite, memory, git-worktree, pm, filesystem)",
			RequiredDeps: []string{"claude"},
		},
		{
			Name:         "telegram",
			Description:  "Telegram forum notifications",
			RequiredDeps: []string{"curl"},
			SetupFunc: func(ctx context.Context, projectRoot string) error {
				wiz := bridge.NewTelegramWizard(projectRoot)
				return wiz.Run(ctx, projectRoot)
			},
		},
		{
			Name:         "docker-sandbox",
			Description:  "Docker-based agent sandboxing",
			RequiredDeps: []string{"docker"},
		},
		{
			Name:         "loops",
			Description:  "Loop scaffolding (research, specs, ideas, creativity)",
			RequiredDeps: []string{"go", "claude"},
		},
	}
}

// featureAvailability holds a feature and whether its deps are satisfied.
type featureAvailability struct {
	Feature   Feature
	Available bool
	Missing   []string // dep names that are missing
}

// CheckFeatureAvailability evaluates which features have their dependencies satisfied.
func CheckFeatureAvailability(features []Feature, scan *ScanResult) []featureAvailability {
	// Build a set of found binaries
	found := make(map[string]bool)
	for _, d := range scan.Dependencies {
		if d.Status == StatusFound {
			found[d.Binary] = true
		}
	}

	result := make([]featureAvailability, len(features))
	for i, f := range features {
		fa := featureAvailability{Feature: f, Available: true}
		for _, dep := range f.RequiredDeps {
			if !found[dep] {
				fa.Available = false
				fa.Missing = append(fa.Missing, dep)
			}
		}
		result[i] = fa
	}
	return result
}

// SelectNonInteractive returns all features whose dependencies are satisfied.
func SelectNonInteractive(features []Feature, scan *ScanResult) []Feature {
	avail := CheckFeatureAvailability(features, scan)
	var selected []Feature
	for _, fa := range avail {
		if fa.Available {
			selected = append(selected, fa.Feature)
		}
	}
	return selected
}

// SelectFromFlags maps the legacy --all, --claude-code, --mcp, --loops flags
// to the feature set for backward compatibility.
func SelectFromFlags(claudeCode, mcp, loops, all bool) []Feature {
	features := DefaultFeatures()
	var selected []Feature

	// Core is always included
	selected = append(selected, features[0])

	if claudeCode || all {
		selected = append(selected, features[1]) // claude-integration
	}
	if mcp || all {
		selected = append(selected, features[2]) // mcp-servers
	}
	if loops || all {
		selected = append(selected, features[5]) // loops
	}
	return selected
}

// FeaturePicker presents an interactive Huh multi-select form for feature selection.
// Features with unsatisfied dependencies are shown but cannot be selected.
func FeaturePicker(features []Feature, scan *ScanResult) ([]Feature, error) {
	avail := CheckFeatureAvailability(features, scan)

	// Build options for the multi-select
	var options []huh.Option[string]
	preSelected := []string{}

	for _, fa := range avail {
		label := fa.Feature.Name + " — " + fa.Feature.Description
		if !fa.Available {
			label += fmt.Sprintf(" [unavailable: missing %v]", fa.Missing)
		}
		opt := huh.NewOption(label, fa.Feature.Name)
		options = append(options, opt)

		// Pre-select available features (core is always selected)
		if fa.Available {
			preSelected = append(preSelected, fa.Feature.Name)
		}
	}

	var chosen []string
	chosen = append(chosen, preSelected...)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Feature Selection").
				Description("Select features to enable. Unavailable features have missing dependencies — run 'orchestra doctor' for details."),
			huh.NewMultiSelect[string]().
				Title("Enable features").
				Options(options...).
				Value(&chosen),
		),
	)

	if err := form.Run(); err != nil {
		return nil, err
	}

	// Filter out unavailable features that the user somehow selected
	availMap := make(map[string]bool)
	for _, fa := range avail {
		if fa.Available {
			availMap[fa.Feature.Name] = true
		}
	}

	// Build a feature map for lookup
	featureMap := make(map[string]Feature)
	for _, fa := range avail {
		featureMap[fa.Feature.Name] = fa.Feature
	}

	var selected []Feature
	for _, name := range chosen {
		if availMap[name] {
			f := featureMap[name]
			f.Enabled = true
			selected = append(selected, f)
		}
	}

	return selected, nil
}

// RunFeatureSetup runs each selected feature's SetupFunc in order.
func RunFeatureSetup(ctx context.Context, projectRoot string, selected []Feature) error {
	for _, f := range selected {
		if f.SetupFunc != nil {
			if err := f.SetupFunc(ctx, projectRoot); err != nil {
				return fmt.Errorf("setup %s: %w", f.Name, err)
			}
		}
	}
	return nil
}

// FeaturesToScaffoldFlags converts selected features to the legacy scaffold boolean flags.
func FeaturesToScaffoldFlags(selected []Feature) (writeClaude, writeMCP, writeLoops bool) {
	for _, f := range selected {
		switch f.Name {
		case "claude-integration":
			writeClaude = true
		case "mcp-servers":
			writeMCP = true
		case "loops":
			writeLoops = true
		}
	}
	return
}
