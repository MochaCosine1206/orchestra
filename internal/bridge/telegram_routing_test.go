package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectRouter_RouteMessage(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "telegram-routing.json")

	r := &ProjectRouter{
		path: path,
		config: RoutingConfig{
			Projects: map[string]ProjectMapping{
				"my-project": {
					TopicID:     42,
					ProjectPath: "/home/user/my-project",
					DisplayName: "My Project",
				},
				"other": {
					TopicID:     99,
					ProjectPath: "/home/user/other",
				},
			},
		},
	}

	// Known topic
	got, err := r.RouteMessage(42)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/home/user/my-project" {
		t.Errorf("expected /home/user/my-project, got %s", got)
	}

	// Unknown topic
	_, err = r.RouteMessage(999)
	if err == nil {
		t.Fatal("expected error for unknown topic")
	}
	if got := err.Error(); got == "" {
		t.Error("expected non-empty error message")
	}
}

func TestProjectRouter_RegisterAndPersist(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "telegram-routing.json")

	r := &ProjectRouter{
		path: path,
		config: RoutingConfig{
			Projects: make(map[string]ProjectMapping),
		},
	}

	if err := r.RegisterProject("test-proj", 100, "/tmp/test-proj", "Test Project"); err != nil {
		t.Fatalf("RegisterProject failed: %v", err)
	}

	// Verify persisted
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("config file is empty")
	}

	// RouteMessage should work
	got, err := r.RouteMessage(100)
	if err != nil {
		t.Fatalf("RouteMessage failed after register: %v", err)
	}
	if got != "/tmp/test-proj" {
		t.Errorf("expected /tmp/test-proj, got %s", got)
	}
}

func TestProjectRouter_SetDefault(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "telegram-routing.json")

	r := &ProjectRouter{
		path: path,
		config: RoutingConfig{
			Projects: map[string]ProjectMapping{
				"proj-a": {TopicID: 1, ProjectPath: "/a"},
				"proj-b": {TopicID: 2, ProjectPath: "/b"},
			},
		},
	}

	if err := r.SetDefault("proj-a"); err != nil {
		t.Fatalf("SetDefault failed: %v", err)
	}
	if got := r.GetDefault(); got != "proj-a" {
		t.Errorf("expected proj-a, got %s", got)
	}

	// Unknown project
	if err := r.SetDefault("nonexistent"); err == nil {
		t.Error("expected error for nonexistent project")
	}
}

func TestProjectRouter_SlugForTopic(t *testing.T) {
	t.Parallel()

	r := &ProjectRouter{
		config: RoutingConfig{
			Projects: map[string]ProjectMapping{
				"alpha": {TopicID: 10, ProjectPath: "/alpha"},
			},
		},
	}

	if got := r.SlugForTopic(10); got != "alpha" {
		t.Errorf("expected alpha, got %q", got)
	}
	if got := r.SlugForTopic(999); got != "" {
		t.Errorf("expected empty string for unknown topic, got %q", got)
	}
}

func TestHandleCrossProjectCommand_Status(t *testing.T) {
	t.Parallel()

	r := &ProjectRouter{
		config: RoutingConfig{
			Projects: map[string]ProjectMapping{
				"proj-a": {TopicID: 1, ProjectPath: "/a", DisplayName: "Project A"},
			},
			DefaultProject: "proj-a",
		},
	}

	resp, handled := HandleCrossProjectCommand("/status", r)
	if !handled {
		t.Fatal("expected /status to be handled")
	}
	if resp == "" {
		t.Error("expected non-empty response")
	}
}

func TestHandleCrossProjectCommand_List(t *testing.T) {
	t.Parallel()

	r := &ProjectRouter{
		config: RoutingConfig{
			Projects: map[string]ProjectMapping{
				"proj-a": {TopicID: 1, ProjectPath: "/a", DisplayName: "Project A"},
				"proj-b": {TopicID: 2, ProjectPath: "/b"},
			},
			DefaultProject: "proj-a",
		},
	}

	resp, handled := HandleCrossProjectCommand("/list", r)
	if !handled {
		t.Fatal("expected /list to be handled")
	}
	if resp == "" {
		t.Error("expected non-empty response")
	}
}

func TestHandleCrossProjectCommand_Switch(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	r := &ProjectRouter{
		path: filepath.Join(tmpDir, "routing.json"),
		config: RoutingConfig{
			Projects: map[string]ProjectMapping{
				"proj-a": {TopicID: 1, ProjectPath: "/a"},
				"proj-b": {TopicID: 2, ProjectPath: "/b"},
			},
		},
	}

	resp, handled := HandleCrossProjectCommand("/switch proj-b", r)
	if !handled {
		t.Fatal("expected /switch to be handled")
	}
	if r.GetDefault() != "proj-b" {
		t.Errorf("expected default to be proj-b, got %s", r.GetDefault())
	}
	if resp == "" {
		t.Error("expected non-empty response")
	}
}

func TestHandleCrossProjectCommand_SwitchUnknown(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()

	r := &ProjectRouter{
		path: filepath.Join(tmpDir, "routing.json"),
		config: RoutingConfig{
			Projects: map[string]ProjectMapping{
				"proj-a": {TopicID: 1, ProjectPath: "/a"},
			},
		},
	}

	resp, handled := HandleCrossProjectCommand("/switch nonexistent", r)
	if !handled {
		t.Fatal("expected /switch to be handled")
	}
	if r.GetDefault() != "" {
		t.Error("expected default to remain empty")
	}
	_ = resp
}

func TestHandleCrossProjectCommand_Unhandled(t *testing.T) {
	t.Parallel()

	r := &ProjectRouter{
		config: RoutingConfig{Projects: make(map[string]ProjectMapping)},
	}

	_, handled := HandleCrossProjectCommand("/other", r)
	if handled {
		t.Error("expected /other to not be handled")
	}
}

func TestHandleCrossProjectCommand_EmptyStatus(t *testing.T) {
	t.Parallel()

	r := &ProjectRouter{
		config: RoutingConfig{Projects: make(map[string]ProjectMapping)},
	}

	resp, handled := HandleCrossProjectCommand("/status", r)
	if !handled {
		t.Fatal("expected /status to be handled")
	}
	if resp == "" {
		t.Error("expected non-empty response for empty project list")
	}
}

func TestHandleUnknownTopic(t *testing.T) {
	t.Parallel()

	resp := HandleUnknownTopic(12345)
	if resp == "" {
		t.Error("expected non-empty response")
	}
}
