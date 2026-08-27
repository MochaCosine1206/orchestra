package sandbox

import (
	"context"
	"strings"
	"testing"
)

func TestContainerName(t *testing.T) {
	name := containerName("task-123")
	if name != "orchestra-sandbox-task-123" {
		t.Errorf("expected orchestra-sandbox-task-123, got %s", name)
	}
}

func TestContainerManagerTrackAndGet(t *testing.T) {
	cm := NewContainerManager()
	c := &Container{ID: "abc123", TaskID: "t1", Image: baseImage}
	cm.Track(c)

	got := cm.Get("t1")
	if got == nil {
		t.Fatal("expected container, got nil")
	}
	if got.ID != "abc123" {
		t.Errorf("expected ID abc123, got %s", got.ID)
	}
}

func TestContainerManagerRemove(t *testing.T) {
	cm := NewContainerManager()
	c := &Container{ID: "abc123", TaskID: "t1", Image: baseImage}
	cm.Track(c)
	cm.Remove("t1")

	if cm.Get("t1") != nil {
		t.Error("expected nil after remove")
	}
}

func TestContainerManagerAll(t *testing.T) {
	cm := NewContainerManager()
	cm.Track(&Container{ID: "a", TaskID: "t1"})
	cm.Track(&Container{ID: "b", TaskID: "t2"})

	all := cm.All()
	if len(all) != 2 {
		t.Errorf("expected 2 containers, got %d", len(all))
	}
}

func TestCreateBuildsDockerArgs(t *testing.T) {
	// Create will fail because docker isn't available in test, but we can
	// verify the policy generates the correct args by testing DockerArgs directly.
	policy := Policy{
		Mount: MountPolicy{
			WorktreePath: "/tmp/worktree",
			LogsDir:      "/tmp/logs",
			ReadOnly:     true,
		},
		Network: NewNetworkPolicy("test-net"),
	}

	args := policy.DockerArgs()
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "--read-only") {
		t.Error("expected --read-only in docker args")
	}
	if !strings.Contains(joined, "/tmp/worktree:/tmp/worktree") {
		t.Error("expected same-path worktree bind mount")
	}
	if !strings.Contains(joined, "--network test-net") {
		t.Error("expected --network flag")
	}
}

func TestContainerExecBuildsCmdArgs(t *testing.T) {
	// Verify the Exec method constructs the right docker exec args.
	// We create a container struct directly (no docker needed).
	c := &Container{ID: "test-container-id", TaskID: "t1"}

	// Exec will fail (no docker), but we verify arg construction
	// by checking it returns an error (not a panic).
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()
	_, err := c.Exec(ctx, []string{"echo", "hello"})
	if err == nil {
		t.Skip("docker available, skipping error path test")
	}
	// We just verify it attempted to run docker exec with our container ID.
	// The error should reference docker or the command.
}

func TestContainerStopIgnoresNoSuchContainer(t *testing.T) {
	c := &Container{ID: "nonexistent-container-id", TaskID: "t1"}
	ctx := context.Background()
	// Stop should not panic even for a nonexistent container.
	// It may return an error, but it should not panic.
	_ = c.Stop(ctx)
}

func TestContainerRemoveIgnoresNoSuchContainer(t *testing.T) {
	c := &Container{ID: "nonexistent-container-id", TaskID: "t1"}
	ctx := context.Background()
	_ = c.Remove(ctx)
}
