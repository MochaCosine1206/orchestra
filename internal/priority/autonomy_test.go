package priority

import "testing"

func autonomyPtr(a AutonomyLevel) *AutonomyLevel { return &a }

func TestResolveAutonomy_DefaultManual(t *testing.T) {
	got := ResolveAutonomy(AutonomyOverrides{}, 0.9)
	if got != Manual {
		t.Errorf("got %d, want Manual(%d)", got, Manual)
	}
}

func TestResolveAutonomy_GlobalOnly(t *testing.T) {
	got := ResolveAutonomy(AutonomyOverrides{Global: autonomyPtr(Full)}, 0.9)
	if got != Full {
		t.Errorf("got %d, want Full(%d)", got, Full)
	}
}

func TestResolveAutonomy_ProjectOverridesGlobal(t *testing.T) {
	got := ResolveAutonomy(AutonomyOverrides{
		Global:  autonomyPtr(Full),
		Project: autonomyPtr(Semi),
	}, 0.9)
	if got != Semi {
		t.Errorf("got %d, want Semi(%d)", got, Semi)
	}
}

func TestResolveAutonomy_ScheduleOverridesProject(t *testing.T) {
	got := ResolveAutonomy(AutonomyOverrides{
		Project:  autonomyPtr(Manual),
		Schedule: autonomyPtr(Full),
	}, 0.9)
	if got != Full {
		t.Errorf("got %d, want Full(%d)", got, Full)
	}
}

func TestResolveAutonomy_ActionOverridesAll(t *testing.T) {
	got := ResolveAutonomy(AutonomyOverrides{
		Global:   autonomyPtr(Manual),
		Project:  autonomyPtr(Manual),
		Schedule: autonomyPtr(Manual),
		Action:   autonomyPtr(Full),
	}, 0.9)
	if got != Full {
		t.Errorf("got %d, want Full(%d)", got, Full)
	}
}

func TestResolveAutonomy_LowTrustForcesManual(t *testing.T) {
	got := ResolveAutonomy(AutonomyOverrides{Action: autonomyPtr(Full)}, 0.2)
	if got != Manual {
		t.Errorf("trust=0.2: got %d, want Manual(%d)", got, Manual)
	}
}

func TestResolveAutonomy_MediumTrustCapsSemi(t *testing.T) {
	got := ResolveAutonomy(AutonomyOverrides{Action: autonomyPtr(Full)}, 0.5)
	if got != Semi {
		t.Errorf("trust=0.5: got %d, want Semi(%d)", got, Semi)
	}
}

func TestResolveAutonomy_MediumTrustAllowsSemi(t *testing.T) {
	got := ResolveAutonomy(AutonomyOverrides{Action: autonomyPtr(Semi)}, 0.5)
	if got != Semi {
		t.Errorf("trust=0.5 with Semi: got %d, want Semi(%d)", got, Semi)
	}
}

func TestResolveAutonomy_HighTrustAllowsFull(t *testing.T) {
	got := ResolveAutonomy(AutonomyOverrides{Action: autonomyPtr(Full)}, 0.9)
	if got != Full {
		t.Errorf("trust=0.9: got %d, want Full(%d)", got, Full)
	}
}

func TestResolveAutonomy_TrustBoundary03(t *testing.T) {
	// Exactly 0.3 should NOT force Manual (only < 0.3 does)
	got := ResolveAutonomy(AutonomyOverrides{Action: autonomyPtr(Full)}, 0.3)
	if got == Manual {
		t.Errorf("trust=0.3 should not force Manual, got %d", got)
	}
}

func TestResolveAutonomy_TrustBoundary06(t *testing.T) {
	// Exactly 0.6 should allow Full (only < 0.6 caps at Semi)
	got := ResolveAutonomy(AutonomyOverrides{Action: autonomyPtr(Full)}, 0.6)
	if got != Full {
		t.Errorf("trust=0.6 should allow Full, got %d", got)
	}
}

func TestShouldRequestApproval_ManualAlwaysTrue(t *testing.T) {
	for _, risk := range []RiskLevel{RiskLow, RiskMedium, RiskHigh, RiskCritical} {
		if !ShouldRequestApproval(Manual, risk) {
			t.Errorf("Manual + risk %d: want true", risk)
		}
	}
}

func TestShouldRequestApproval_SemiLowFalse(t *testing.T) {
	if ShouldRequestApproval(Semi, RiskLow) {
		t.Error("Semi + Low: want false")
	}
}

func TestShouldRequestApproval_SemiMediumTrue(t *testing.T) {
	if !ShouldRequestApproval(Semi, RiskMedium) {
		t.Error("Semi + Medium: want true")
	}
}

func TestShouldRequestApproval_SemiHighTrue(t *testing.T) {
	if !ShouldRequestApproval(Semi, RiskHigh) {
		t.Error("Semi + High: want true")
	}
}

func TestShouldRequestApproval_SemiCriticalTrue(t *testing.T) {
	if !ShouldRequestApproval(Semi, RiskCritical) {
		t.Error("Semi + Critical: want true")
	}
}

func TestShouldRequestApproval_FullLowFalse(t *testing.T) {
	if ShouldRequestApproval(Full, RiskLow) {
		t.Error("Full + Low: want false")
	}
}

func TestShouldRequestApproval_FullMediumFalse(t *testing.T) {
	if ShouldRequestApproval(Full, RiskMedium) {
		t.Error("Full + Medium: want false")
	}
}

func TestShouldRequestApproval_FullHighFalse(t *testing.T) {
	if ShouldRequestApproval(Full, RiskHigh) {
		t.Error("Full + High: want false")
	}
}

func TestShouldRequestApproval_FullCriticalTrue(t *testing.T) {
	if !ShouldRequestApproval(Full, RiskCritical) {
		t.Error("Full + Critical: want true")
	}
}
