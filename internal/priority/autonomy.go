package priority

// AutonomyLevel represents the degree of autonomous operation.
type AutonomyLevel int

const (
	Manual AutonomyLevel = 0
	Semi   AutonomyLevel = 1
	Full   AutonomyLevel = 2
)

// RiskLevel represents the risk classification of an action.
type RiskLevel int

const (
	RiskLow      RiskLevel = 0
	RiskMedium   RiskLevel = 1
	RiskHigh     RiskLevel = 2
	RiskCritical RiskLevel = 3
)

// AutonomyOverrides holds autonomy settings at each override level.
// A nil pointer means "not set" at that level.
type AutonomyOverrides struct {
	Global   *AutonomyLevel
	Project  *AutonomyLevel
	Schedule *AutonomyLevel
	Action   *AutonomyLevel
}

// ResolveAutonomy determines the effective autonomy level by applying
// the override hierarchy (action > schedule > project > global) and
// trust-based capping (trust < 0.3 forces Manual, trust < 0.6 caps at Semi).
func ResolveAutonomy(overrides AutonomyOverrides, trustScore float64) AutonomyLevel {
	// Apply override hierarchy: most specific wins.
	level := Manual // default
	if overrides.Global != nil {
		level = *overrides.Global
	}
	if overrides.Project != nil {
		level = *overrides.Project
	}
	if overrides.Schedule != nil {
		level = *overrides.Schedule
	}
	if overrides.Action != nil {
		level = *overrides.Action
	}

	// Trust-based capping.
	if trustScore < 0.3 {
		return Manual
	}
	if trustScore < 0.6 && level > Semi {
		return Semi
	}
	return level
}

// ShouldRequestApproval determines whether user approval is needed
// given the current autonomy level and risk level.
// Manual: always true. Semi: true for medium, high, critical. Full: true only for critical.
func ShouldRequestApproval(level AutonomyLevel, risk RiskLevel) bool {
	switch level {
	case Manual:
		return true
	case Semi:
		return risk >= RiskMedium
	case Full:
		return risk >= RiskCritical
	default:
		return true
	}
}
