package sandbox

import "fmt"

// MountPolicy configures filesystem mounts for a sandboxed container.
type MountPolicy struct {
	// WorktreePath is bind-mounted at the same absolute path inside the container (RW).
	WorktreePath string
	// LogsDir is bind-mounted at the same path inside the container (RW).
	LogsDir string
	// ReadOnly enables --read-only on the root filesystem.
	ReadOnly bool
}

// DockerArgs returns the docker run flags for this mount policy.
func (m MountPolicy) DockerArgs() []string {
	var args []string
	if m.ReadOnly {
		args = append(args, "--read-only")
	}
	if m.WorktreePath != "" {
		args = append(args, "-v", fmt.Sprintf("%s:%s", m.WorktreePath, m.WorktreePath))
	}
	if m.LogsDir != "" {
		args = append(args, "-v", fmt.Sprintf("%s:%s", m.LogsDir, m.LogsDir))
	}
	// RW /tmp mount so agents can write temp files despite read-only root.
	args = append(args, "--tmpfs", "/tmp:rw,exec,size=512m")
	return args
}
