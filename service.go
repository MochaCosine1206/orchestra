package orchestra

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/MochaCosine1206/orchestra/internal/config"
	"github.com/kardianos/service"
)

// svcConfig returns the kardianos/service config for user-level installation.
func svcConfig() *service.Config {
	return &service.Config{
		Name:        "orchestra-daemon",
		DisplayName: "Orchestra Daemon",
		Description: "Claude Orchestra background daemon — schedules and monitors orchestration sessions.",
		Option: service.KeyValue{
			// macOS: install as user-level LaunchAgent (no sudo required).
			"UserService": true,
		},
	}
}

// NewService creates a kardianos/service.Service using the given Interface implementation.
func NewService(p service.Interface) (service.Service, error) {
	return service.New(p, svcConfig())
}

// Install registers the daemon as a user-level OS service (no sudo required).
func Install() error {
	svc, err := NewService(&daemonProgram{})
	if err != nil {
		return fmt.Errorf("creating service: %w", err)
	}
	return svc.Install()
}

// Uninstall removes the daemon from the OS service manager.
func Uninstall() error {
	svc, err := NewService(&daemonProgram{})
	if err != nil {
		return fmt.Errorf("creating service: %w", err)
	}
	return svc.Uninstall()
}

// StartService starts the daemon via the OS service manager.
func StartService() error {
	svc, err := NewService(&daemonProgram{})
	if err != nil {
		return fmt.Errorf("creating service: %w", err)
	}
	return svc.Start()
}

// StopService stops the daemon via the OS service manager.
func StopService() error {
	svc, err := NewService(&daemonProgram{})
	if err != nil {
		return fmt.Errorf("creating service: %w", err)
	}
	return svc.Stop()
}

// ServiceStatus returns the current service status.
func ServiceStatus() (service.Status, error) {
	svc, err := NewService(&daemonProgram{})
	if err != nil {
		return service.StatusUnknown, fmt.Errorf("creating service: %w", err)
	}
	return svc.Status()
}

// daemonProgram implements service.Interface for kardianos/service.
type daemonProgram struct{}

func (p *daemonProgram) Start(s service.Service) error {
	go p.run()
	return nil
}

func (p *daemonProgram) run() {
	// The actual daemon loop will be implemented in a future phase.
	// For now, this is a placeholder that the service manager can start/stop.
	select {}
}

func (p *daemonProgram) Stop(s service.Service) error {
	return nil
}

// LockPath returns the path to the daemon singleton lock file.
func LockPath() (string, error) {
	dir, err := config.GlobalDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "daemon.lock"), nil
}

// AcquireLock acquires an exclusive flock on the daemon lock file.
// Returns the file descriptor on success. The lock is released when the
// file descriptor is closed or the process exits.
func AcquireLock() (int, error) {
	lockPath, err := LockPath()
	if err != nil {
		return -1, fmt.Errorf("resolving lock path: %w", err)
	}

	fd, err := syscall.Open(lockPath, syscall.O_CREAT|syscall.O_RDWR, 0o644)
	if err != nil {
		return -1, fmt.Errorf("opening lock file %s: %w", lockPath, err)
	}

	// Try non-blocking exclusive lock.
	err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		syscall.Close(fd)
		return -1, fmt.Errorf("another daemon instance is already running (lock: %s)", lockPath)
	}

	// Write our PID into the lock file for diagnostics.
	pid := fmt.Sprintf("%d\n", os.Getpid())
	syscall.Write(fd, []byte(pid))

	return fd, nil
}

// ReleaseLock releases the daemon singleton lock.
func ReleaseLock(fd int) {
	if fd >= 0 {
		syscall.Flock(fd, syscall.LOCK_UN)
		syscall.Close(fd)
	}

	// Best-effort cleanup of the lock file.
	if lockPath, err := LockPath(); err == nil {
		os.Remove(lockPath)
	}
}
