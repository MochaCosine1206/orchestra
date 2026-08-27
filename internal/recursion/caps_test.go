package recursion

import "testing"

func TestMaxAgents_Depth0(t *testing.T) {
	if got := MaxAgents(0); got != 8 {
		t.Errorf("MaxAgents(0) = %d, want 8", got)
	}
}

func TestMaxAgents_Depth1(t *testing.T) {
	if got := MaxAgents(1); got != 3 {
		t.Errorf("MaxAgents(1) = %d, want 3", got)
	}
}

func TestMaxAgents_Depth2(t *testing.T) {
	if got := MaxAgents(2); got != 1 {
		t.Errorf("MaxAgents(2) = %d, want 1", got)
	}
}

func TestMaxAgents_Depth3(t *testing.T) {
	if got := MaxAgents(3); got != 1 {
		t.Errorf("MaxAgents(3) = %d, want 1", got)
	}
}

func TestMaxAgents_NegativeDepth(t *testing.T) {
	if got := MaxAgents(-1); got != 8 {
		t.Errorf("MaxAgents(-1) = %d, want 8", got)
	}
}
