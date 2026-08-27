package sandbox

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	cmdTimeout = 30 * time.Second
	baseImage  = "orchestra-agent:latest"
)

// Container represents a Docker container managed by the sandbox.
type Container struct {
	ID     string
	TaskID string
	Image  string
	policy Policy
}

// ContainerManager tracks containers keyed by task ID for lifecycle management.
type ContainerManager struct {
	mu         sync.Mutex
	containers map[string]*Container
}

// NewContainerManager returns an initialized ContainerManager.
func NewContainerManager() *ContainerManager {
	return &ContainerManager{
		containers: make(map[string]*Container),
	}
}

// Get returns the container for a task ID, or nil.
func (cm *ContainerManager) Get(taskID string) *Container {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	return cm.containers[taskID]
}

// Track registers a container for a task ID.
func (cm *ContainerManager) Track(c *Container) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.containers[c.TaskID] = c
}

// Remove unregisters a container for a task ID.
func (cm *ContainerManager) Remove(taskID string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	delete(cm.containers, taskID)
}

// All returns all tracked containers.
func (cm *ContainerManager) All() []*Container {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	out := make([]*Container, 0, len(cm.containers))
	for _, c := range cm.containers {
		out = append(out, c)
	}
	return out
}

// ImageExists checks whether the given image exists locally.
func ImageExists(ctx context.Context, image string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", image)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}
	// docker image inspect exits non-zero if the image doesn't exist.
	return false, nil
}

// EnsureImage pulls the base image if it doesn't exist locally.
func EnsureImage(ctx context.Context, image string) error {
	exists, err := ImageExists(ctx, image)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "pull", image)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("pull image %s: %w: %s", image, err, out)
	}
	return nil
}

// Create builds a new container (without starting it) using the given policy.
func Create(ctx context.Context, taskID string, policy Policy) (*Container, error) {
	image := baseImage

	args := []string{"create"}
	args = append(args, "--name", containerName(taskID))
	args = append(args, policy.DockerArgs()...)
	args = append(args, image)

	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker create: %w: %s", err, stderr.String())
	}

	id := strings.TrimSpace(stdout.String())
	return &Container{
		ID:     id,
		TaskID: taskID,
		Image:  image,
		policy: policy,
	}, nil
}

// Start starts a previously created container.
func (c *Container) Start(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "start", c.ID)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker start %s: %w: %s", c.ID, err, out)
	}
	return nil
}

// Exec runs a command inside the running container and returns its combined output.
// execTimeout is the timeout for agent execution inside containers.
// Much longer than cmdTimeout since agents run for minutes to hours.
const execTimeout = 45 * time.Minute

func (c *Container) Exec(ctx context.Context, command []string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, execTimeout)
	defer cancel()
	args := append([]string{"exec", c.ID}, command...)
	cmd := exec.CommandContext(ctx, "docker", args...)
	return cmd.CombinedOutput()
}

// Stop stops the container with a grace period.
func (c *Container) Stop(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "stop", "--time", "10", c.ID)
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such container") {
		return fmt.Errorf("docker stop %s: %w: %s", c.ID, err, out)
	}
	return nil
}

// Remove forcefully removes the container.
func (c *Container) Remove(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, cmdTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "rm", "--force", c.ID)
	out, err := cmd.CombinedOutput()
	if err != nil && !strings.Contains(string(out), "No such container") {
		return fmt.Errorf("docker rm %s: %w: %s", c.ID, err, out)
	}
	return nil
}

func containerName(taskID string) string {
	return "orchestra-sandbox-" + taskID
}
