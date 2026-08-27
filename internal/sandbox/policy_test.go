package sandbox

import (
	"strings"
	"testing"
)

func TestPolicyDockerArgsIncludesReadOnly(t *testing.T) {
	p := Policy{
		Mount: MountPolicy{
			WorktreePath: "/work",
			LogsDir:      "/logs",
			ReadOnly:     true,
		},
	}
	args := p.DockerArgs()
	found := false
	for _, a := range args {
		if a == "--read-only" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected --read-only in args: %v", args)
	}
}

func TestPolicyDockerArgsSamePathMount(t *testing.T) {
	p := Policy{
		Mount: MountPolicy{
			WorktreePath: "/my/worktree",
			ReadOnly:     true,
		},
	}
	args := p.DockerArgs()
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "/my/worktree:/my/worktree") {
		t.Errorf("expected same-path bind mount, got: %s", joined)
	}
}

func TestPolicyDockerArgsTmpfsMount(t *testing.T) {
	p := Policy{
		Mount: MountPolicy{ReadOnly: true},
	}
	args := p.DockerArgs()
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--tmpfs") || !strings.Contains(joined, "/tmp") {
		t.Errorf("expected --tmpfs /tmp mount, got: %s", joined)
	}
}

func TestPolicyDockerArgsWithNetwork(t *testing.T) {
	p := Policy{
		Mount:   MountPolicy{ReadOnly: true},
		Network: NewNetworkPolicy("sandbox-net"),
	}
	args := p.DockerArgs()
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--network sandbox-net") {
		t.Errorf("expected --network flag, got: %s", joined)
	}
}

func TestPolicyDockerArgsWithoutNetwork(t *testing.T) {
	p := Policy{
		Mount: MountPolicy{ReadOnly: true},
	}
	args := p.DockerArgs()
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--network") {
		t.Errorf("unexpected --network flag without network policy: %s", joined)
	}
}
