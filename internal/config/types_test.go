package config

import (
	"encoding/json"
	"testing"
	"time"
)

func TestProjectEntryRoundTrip(t *testing.T) {
	entry := &ProjectEntry{
		Path:           "/home/user/myproject",
		Name:           "myproject",
		LastActive:     time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC),
		AutoRegistered: true,
		Tags:           []string{"go", "active"},
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got ProjectEntry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Path != entry.Path {
		t.Errorf("Path = %q, want %q", got.Path, entry.Path)
	}
	if got.Name != entry.Name {
		t.Errorf("Name = %q, want %q", got.Name, entry.Name)
	}
	if !got.LastActive.Equal(entry.LastActive) {
		t.Errorf("LastActive = %v, want %v", got.LastActive, entry.LastActive)
	}
	if got.AutoRegistered != entry.AutoRegistered {
		t.Errorf("AutoRegistered = %v, want %v", got.AutoRegistered, entry.AutoRegistered)
	}
	if len(got.Tags) != 2 || got.Tags[0] != "go" {
		t.Errorf("Tags = %v, want [go active]", got.Tags)
	}
}

func TestProjectsRegistryRoundTrip(t *testing.T) {
	reg := NewProjectsRegistry()
	reg.Projects["/foo"] = &ProjectEntry{Path: "/foo", Name: "foo"}
	data, err := json.Marshal(reg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got ProjectsRegistry
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Version != 1 {
		t.Errorf("Version = %d, want 1", got.Version)
	}
	if _, ok := got.Projects["/foo"]; !ok {
		t.Error("missing /foo in projects")
	}
}

func TestDaemonConfigRoundTrip(t *testing.T) {
	cfg := &DaemonConfig{
		Interval:      "10m",
		MaxConcurrent: 4,
		QuietHours:    &QuietHoursConfig{Start: "22:00", End: "08:00"},
		Schedules: []ScheduleEntry{
			{Project: "myproj", Cron: "0 9 * * *", Goal: "run tests"},
		},
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var got DaemonConfig
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got.Interval != "10m" {
		t.Errorf("Interval = %q, want 10m", got.Interval)
	}
	if got.MaxConcurrent != 4 {
		t.Errorf("MaxConcurrent = %d, want 4", got.MaxConcurrent)
	}
	if got.QuietHours == nil || got.QuietHours.Start != "22:00" {
		t.Errorf("QuietHours = %+v, want start 22:00", got.QuietHours)
	}
	if len(got.Schedules) != 1 || got.Schedules[0].Project != "myproj" {
		t.Errorf("Schedules = %+v, want 1 entry for myproj", got.Schedules)
	}
}

func TestDefaultDaemonConfig(t *testing.T) {
	cfg := DefaultDaemonConfig()
	if cfg.Interval != "5m" {
		t.Errorf("default Interval = %q, want 5m", cfg.Interval)
	}
	if cfg.MaxConcurrent != 2 {
		t.Errorf("default MaxConcurrent = %d, want 2", cfg.MaxConcurrent)
	}
}
