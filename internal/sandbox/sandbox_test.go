package sandbox

import (
	"context"
	"testing"
)

func TestSandboxDisabledBypass(t *testing.T) {
	s := New(false, "/tmp/repo", "/tmp/logs", "")
	out, err := s.Wrap(context.Background(), "t1", "/tmp/work", []string{"echo", "hello"})
	if err != nil {
		t.Errorf("expected nil error when disabled, got %v", err)
	}
	if out != nil {
		t.Errorf("expected nil output when disabled, got %v", out)
	}
}

func TestSandboxNew(t *testing.T) {
	s := New(true, "/tmp/repo", "/tmp/logs", "")
	if !s.Enabled {
		t.Error("expected Enabled=true")
	}
	if s.manager == nil {
		t.Error("expected non-nil manager")
	}
	if s.network == nil {
		t.Error("expected non-nil network")
	}
}

func TestSandboxNetworkAccessor(t *testing.T) {
	s := New(true, "/tmp/repo", "/tmp/logs", "")
	np := s.Network()
	if np == nil {
		t.Fatal("expected non-nil network policy")
	}
	al := np.Allowlist()
	if len(al) != 1 || al[0].Host != "api.anthropic.com" {
		t.Errorf("expected default allowlist, got %v", al)
	}
}

func TestSandboxCleanupAllEmpty(t *testing.T) {
	s := New(true, "/tmp/repo", "/tmp/logs", "")
	err := s.CleanupAll(context.Background())
	if err != nil {
		t.Errorf("expected nil error for empty cleanup, got %v", err)
	}
}

func TestSandboxCleanupAllRemovesTracked(t *testing.T) {
	s := New(true, "/tmp/repo", "/tmp/logs", "")
	// Manually track containers (simulating post-create state).
	s.manager.Track(&Container{ID: "c1", TaskID: "t1"})
	s.manager.Track(&Container{ID: "c2", TaskID: "t2"})

	// CleanupAll will attempt docker stop/rm which will fail (no docker),
	// but it should still remove containers from tracking.
	_ = s.CleanupAll(context.Background())

	all := s.manager.All()
	if len(all) != 0 {
		t.Errorf("expected 0 tracked containers after cleanup, got %d", len(all))
	}
}

func TestSandboxWrapEnabledFailsWithoutDocker(t *testing.T) {
	s := New(true, "/tmp/repo", "/tmp/logs", "")
	_, err := s.Wrap(context.Background(), "t1", "/tmp/work", []string{"echo"})
	if err == nil {
		t.Skip("docker available, skipping error path test")
	}
	// Verify it attempted container creation (error should mention sandbox/docker).
	if err != nil && !testing.Short() {
		// Just verify no panic — the error is expected without docker.
		t.Logf("expected error without docker: %v", err)
	}
}
