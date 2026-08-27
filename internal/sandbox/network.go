package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const defaultTimeout = 30 * time.Second

// HostPort represents an allowed network destination.
type HostPort struct {
	Host string
	Port int
}

func (hp HostPort) String() string {
	return fmt.Sprintf("%s:%d", hp.Host, hp.Port)
}

// NetworkPolicy controls which external hosts a sandboxed container can reach.
type NetworkPolicy struct {
	mu        sync.Mutex
	allowlist []HostPort
	// NetworkName is the docker network name created for this policy.
	NetworkName string
}

// NewNetworkPolicy returns a policy with the default allowlist (api.anthropic.com:443).
func NewNetworkPolicy(networkName string) *NetworkPolicy {
	return &NetworkPolicy{
		NetworkName: networkName,
		allowlist: []HostPort{
			{Host: "api.anthropic.com", Port: 443},
		},
	}
}

// Allowlist returns a copy of the current allowlist.
func (n *NetworkPolicy) Allowlist() []HostPort {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]HostPort, len(n.allowlist))
	copy(out, n.allowlist)
	return out
}

// Allow adds a host:port pair to the allowlist.
func (n *NetworkPolicy) Allow(host string, port int) {
	n.mu.Lock()
	defer n.mu.Unlock()
	for _, hp := range n.allowlist {
		if hp.Host == host && hp.Port == port {
			return
		}
	}
	n.allowlist = append(n.allowlist, HostPort{Host: host, Port: port})
}

// Deny clears the entire allowlist, blocking all network access.
func (n *NetworkPolicy) Deny() {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.allowlist = nil
}

// CreateNetwork creates the docker network. Idempotent — ignores "already exists" errors.
func (n *NetworkPolicy) CreateNetwork(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "network", "create", n.NetworkName)
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "already exists") {
		return fmt.Errorf("create network %s: %w: %s", n.NetworkName, err, out)
	}
	return nil
}

// RemoveNetwork removes the docker network.
func (n *NetworkPolicy) RemoveNetwork(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "network", "rm", n.NetworkName)
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "not found") {
		return fmt.Errorf("remove network %s: %w: %s", n.NetworkName, err, out)
	}
	return nil
}

// DockerArgs returns the docker run flags for network policy.
func (n *NetworkPolicy) DockerArgs() []string {
	if n.NetworkName == "" {
		return nil
	}
	return []string{"--network", n.NetworkName}
}
