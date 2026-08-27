package daemon

import (
	"testing"
	"time"
)

func TestParseCron_Wildcard(t *testing.T) {
	c, err := ParseCron("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Minute) != 60 {
		t.Errorf("expected 60 minutes, got %d", len(c.Minute))
	}
	if len(c.Hour) != 24 {
		t.Errorf("expected 24 hours, got %d", len(c.Hour))
	}
}

func TestParseCron_Specific(t *testing.T) {
	c, err := ParseCron("30 14 1 6 3")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Minute) != 1 || c.Minute[0] != 30 {
		t.Errorf("expected minute [30], got %v", c.Minute)
	}
	if len(c.Hour) != 1 || c.Hour[0] != 14 {
		t.Errorf("expected hour [14], got %v", c.Hour)
	}
	if len(c.DayOfMonth) != 1 || c.DayOfMonth[0] != 1 {
		t.Errorf("expected dom [1], got %v", c.DayOfMonth)
	}
	if len(c.Month) != 1 || c.Month[0] != 6 {
		t.Errorf("expected month [6], got %v", c.Month)
	}
	if len(c.DayOfWeek) != 1 || c.DayOfWeek[0] != 3 {
		t.Errorf("expected dow [3], got %v", c.DayOfWeek)
	}
}

func TestParseCron_Step(t *testing.T) {
	c, err := ParseCron("*/15 * * * *")
	if err != nil {
		t.Fatal(err)
	}
	expected := []int{0, 15, 30, 45}
	if len(c.Minute) != len(expected) {
		t.Fatalf("expected %d minutes, got %d", len(expected), len(c.Minute))
	}
	for i, v := range expected {
		if c.Minute[i] != v {
			t.Errorf("minute[%d]: expected %d, got %d", i, v, c.Minute[i])
		}
	}
}

func TestParseCron_Range(t *testing.T) {
	c, err := ParseCron("0 9-17 * * *")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Hour) != 9 {
		t.Errorf("expected 9 hours (9-17), got %d", len(c.Hour))
	}
	if c.Hour[0] != 9 || c.Hour[8] != 17 {
		t.Errorf("expected hours 9-17, got %v", c.Hour)
	}
}

func TestParseCron_List(t *testing.T) {
	c, err := ParseCron("0 0 1,15 * *")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.DayOfMonth) != 2 || c.DayOfMonth[0] != 1 || c.DayOfMonth[1] != 15 {
		t.Errorf("expected dom [1,15], got %v", c.DayOfMonth)
	}
}

func TestParseCron_RangeWithStep(t *testing.T) {
	c, err := ParseCron("0 0-23/6 * * *")
	if err != nil {
		t.Fatal(err)
	}
	expected := []int{0, 6, 12, 18}
	if len(c.Hour) != len(expected) {
		t.Fatalf("expected %d hours, got %d: %v", len(expected), len(c.Hour), c.Hour)
	}
	for i, v := range expected {
		if c.Hour[i] != v {
			t.Errorf("hour[%d]: expected %d, got %d", i, v, c.Hour[i])
		}
	}
}

func TestParseCron_Presets(t *testing.T) {
	tests := []struct {
		preset string
		minute int
		hour   int
	}{
		{"@hourly", 0, -1},   // minute=0, hour=*
		{"@daily", 0, 0},     // 0 0 * * *
		{"@midnight", 0, 0},  // same as daily
		{"@weekly", 0, 0},    // 0 0 * * 0
		{"@monthly", 0, 0},   // 0 0 1 * *
		{"@yearly", 0, 0},    // 0 0 1 1 *
		{"@annually", 0, 0},  // same as yearly
	}
	for _, tt := range tests {
		c, err := ParseCron(tt.preset)
		if err != nil {
			t.Errorf("%s: %v", tt.preset, err)
			continue
		}
		if c.Minute[0] != tt.minute {
			t.Errorf("%s: minute[0]=%d, want %d", tt.preset, c.Minute[0], tt.minute)
		}
		if tt.hour >= 0 && c.Hour[0] != tt.hour {
			t.Errorf("%s: hour[0]=%d, want %d", tt.preset, c.Hour[0], tt.hour)
		}
	}
}

func TestParseCron_InvalidFieldCount(t *testing.T) {
	_, err := ParseCron("* * *")
	if err == nil {
		t.Error("expected error for 3-field expression")
	}
}

func TestParseCron_InvalidValue(t *testing.T) {
	_, err := ParseCron("60 * * * *")
	if err == nil {
		t.Error("expected error for minute=60")
	}
}

func TestIsDue_Match(t *testing.T) {
	c, _ := ParseCron("30 14 * * *")
	// 2026-03-25 14:30 (Tuesday=2)
	tm := time.Date(2026, 3, 25, 14, 30, 0, 0, time.UTC)
	if !c.IsDue(tm) {
		t.Error("expected IsDue=true for 14:30")
	}
}

func TestIsDue_NoMatch(t *testing.T) {
	c, _ := ParseCron("30 14 * * *")
	tm := time.Date(2026, 3, 25, 14, 31, 0, 0, time.UTC)
	if c.IsDue(tm) {
		t.Error("expected IsDue=false for 14:31")
	}
}

func TestIsDue_DayOfWeek(t *testing.T) {
	c, _ := ParseCron("0 9 * * 1") // Monday only
	monday := time.Date(2026, 3, 23, 9, 0, 0, 0, time.UTC)    // Monday
	tuesday := time.Date(2026, 3, 24, 9, 0, 0, 0, time.UTC)   // Tuesday
	if !c.IsDue(monday) {
		t.Error("expected IsDue=true on Monday")
	}
	if c.IsDue(tuesday) {
		t.Error("expected IsDue=false on Tuesday")
	}
}

func TestCheckSchedules(t *testing.T) {
	schedules := []ScheduleConfig{
		{Project: "/a", Cron: "30 14 * * *"},
		{Project: "/b", Cron: "0 9 * * *"},
		{Project: "/c", Cron: "30 14 * * *", Disabled: true},
	}
	now := time.Date(2026, 3, 25, 14, 30, 0, 0, time.UTC)
	due := CheckSchedules(schedules, now)
	if len(due) != 1 || due[0].Project != "/a" {
		t.Errorf("expected [/a] due, got %v", due)
	}
}
