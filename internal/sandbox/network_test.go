package sandbox

import (
	"testing"
)

func TestNewNetworkPolicyDefaultAllowlist(t *testing.T) {
	np := NewNetworkPolicy("test-net")
	al := np.Allowlist()
	if len(al) != 1 {
		t.Fatalf("expected 1 default entry, got %d", len(al))
	}
	if al[0].Host != "api.anthropic.com" || al[0].Port != 443 {
		t.Errorf("expected api.anthropic.com:443, got %s", al[0])
	}
}

func TestNetworkPolicyAllow(t *testing.T) {
	np := NewNetworkPolicy("test-net")
	np.Allow("example.com", 80)
	al := np.Allowlist()
	if len(al) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(al))
	}
	if al[1].Host != "example.com" || al[1].Port != 80 {
		t.Errorf("expected example.com:80, got %s", al[1])
	}
}

func TestNetworkPolicyAllowDedup(t *testing.T) {
	np := NewNetworkPolicy("test-net")
	np.Allow("api.anthropic.com", 443) // already in default
	al := np.Allowlist()
	if len(al) != 1 {
		t.Errorf("expected dedup to 1 entry, got %d", len(al))
	}
}

func TestNetworkPolicyDeny(t *testing.T) {
	np := NewNetworkPolicy("test-net")
	np.Deny()
	al := np.Allowlist()
	if len(al) != 0 {
		t.Errorf("expected empty allowlist after Deny, got %d entries", len(al))
	}
}

func TestNetworkPolicyDockerArgs(t *testing.T) {
	np := NewNetworkPolicy("my-network")
	args := np.DockerArgs()
	if len(args) != 2 || args[0] != "--network" || args[1] != "my-network" {
		t.Errorf("expected [--network my-network], got %v", args)
	}
}

func TestNetworkPolicyDockerArgsEmpty(t *testing.T) {
	np := &NetworkPolicy{}
	args := np.DockerArgs()
	if len(args) != 0 {
		t.Errorf("expected empty args for empty network name, got %v", args)
	}
}

func TestHostPortString(t *testing.T) {
	hp := HostPort{Host: "example.com", Port: 8080}
	if hp.String() != "example.com:8080" {
		t.Errorf("expected example.com:8080, got %s", hp.String())
	}
}
