package daemon

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// CronExpr represents a parsed 5-field cron expression.
// Fields: minute, hour, day-of-month, month, day-of-week.
type CronExpr struct {
	Minute     []int // 0-59
	Hour       []int // 0-23
	DayOfMonth []int // 1-31
	Month      []int // 1-12
	DayOfWeek  []int // 0-6 (Sunday=0)
}

// ParseCron parses a standard 5-field cron expression.
// Supports: *, ranges (1-5), steps (*/5), lists (1,3,5), and presets (@hourly, etc.).
func ParseCron(expr string) (*CronExpr, error) {
	expr = strings.TrimSpace(expr)

	// Handle presets
	switch expr {
	case "@yearly", "@annually":
		expr = "0 0 1 1 *"
	case "@monthly":
		expr = "0 0 1 * *"
	case "@weekly":
		expr = "0 0 * * 0"
	case "@daily", "@midnight":
		expr = "0 0 * * *"
	case "@hourly":
		expr = "0 * * * *"
	}

	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields, got %d", len(fields))
	}

	minute, err := parseField(fields[0], 0, 59)
	if err != nil {
		return nil, fmt.Errorf("minute field: %w", err)
	}
	hour, err := parseField(fields[1], 0, 23)
	if err != nil {
		return nil, fmt.Errorf("hour field: %w", err)
	}
	dom, err := parseField(fields[2], 1, 31)
	if err != nil {
		return nil, fmt.Errorf("day-of-month field: %w", err)
	}
	month, err := parseField(fields[3], 1, 12)
	if err != nil {
		return nil, fmt.Errorf("month field: %w", err)
	}
	dow, err := parseField(fields[4], 0, 6)
	if err != nil {
		return nil, fmt.Errorf("day-of-week field: %w", err)
	}

	return &CronExpr{
		Minute:     minute,
		Hour:       hour,
		DayOfMonth: dom,
		Month:      month,
		DayOfWeek:  dow,
	}, nil
}

// IsDue returns true if the given time matches the cron expression.
func (c *CronExpr) IsDue(t time.Time) bool {
	return contains(c.Minute, t.Minute()) &&
		contains(c.Hour, t.Hour()) &&
		contains(c.DayOfMonth, t.Day()) &&
		contains(c.Month, int(t.Month())) &&
		contains(c.DayOfWeek, int(t.Weekday()))
}

func contains(vals []int, v int) bool {
	for _, x := range vals {
		if x == v {
			return true
		}
	}
	return false
}

// parseField parses a single cron field (e.g., "*/5", "1-3", "1,2,3", "*").
func parseField(field string, min, max int) ([]int, error) {
	var result []int
	for _, part := range strings.Split(field, ",") {
		vals, err := parsePart(part, min, max)
		if err != nil {
			return nil, err
		}
		result = append(result, vals...)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("empty field")
	}
	return result, nil
}

func parsePart(part string, min, max int) ([]int, error) {
	// Handle step: */2, 1-10/2
	step := 1
	if idx := strings.Index(part, "/"); idx != -1 {
		var err error
		step, err = strconv.Atoi(part[idx+1:])
		if err != nil || step <= 0 {
			return nil, fmt.Errorf("invalid step in %q", part)
		}
		part = part[:idx]
	}

	var low, high int

	switch {
	case part == "*":
		low, high = min, max
	case strings.Contains(part, "-"):
		bounds := strings.SplitN(part, "-", 2)
		var err error
		low, err = strconv.Atoi(bounds[0])
		if err != nil {
			return nil, fmt.Errorf("invalid range start in %q", part)
		}
		high, err = strconv.Atoi(bounds[1])
		if err != nil {
			return nil, fmt.Errorf("invalid range end in %q", part)
		}
	default:
		val, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("invalid value %q", part)
		}
		low, high = val, val
	}

	if low < min || high > max || low > high {
		return nil, fmt.Errorf("value out of range [%d-%d] in %q", min, max, part)
	}

	var vals []int
	for i := low; i <= high; i += step {
		vals = append(vals, i)
	}
	return vals, nil
}

// CheckSchedules evaluates schedule entries and returns project paths whose schedules are due.
func CheckSchedules(schedules []ScheduleConfig, now time.Time) []ScheduleConfig {
	var due []ScheduleConfig
	for _, s := range schedules {
		if s.Disabled {
			continue
		}
		expr, err := ParseCron(s.Cron)
		if err != nil {
			continue
		}
		if expr.IsDue(now) {
			due = append(due, s)
		}
	}
	return due
}

// ScheduleConfig mirrors config.ScheduleEntry for daemon use.
type ScheduleConfig struct {
	Project  string
	Cron     string
	Goal     string
	Priority string // "background", "daily", "critical", "fix"
	Disabled bool
}
