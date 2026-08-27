package logstream

import (
	"bufio"
	"context"
	"io"
	"os"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	batchInterval  = 100 * time.Millisecond
	tickerFallback = 500 * time.Millisecond
)

// Streamer tails a log file and sends new lines through a channel.
// It uses fsnotify for efficient file watching with a ticker fallback.
type Streamer struct {
	Lines chan []string // batched line output

	mu       sync.Mutex
	filePath string
	offset   int64
	cancel   context.CancelFunc
	done     chan struct{}
}

// New creates a new Streamer. Call Start to begin watching.
func New() *Streamer {
	return &Streamer{
		Lines: make(chan []string, 64),
	}
}

// Start begins the streaming goroutine. The streamer watches the current file
// (if set) and sends batched lines to the Lines channel.
func (s *Streamer) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.done = make(chan struct{})
	go s.run(ctx)
}

// Stop terminates the streaming goroutine and waits for it to finish.
func (s *Streamer) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
	if s.done != nil {
		<-s.done
	}
}

// SwitchFile changes the file being tailed. Resets offset to 0.
func (s *Streamer) SwitchFile(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.filePath = path
	s.offset = 0
}

// CurrentFile returns the file currently being tailed.
func (s *Streamer) CurrentFile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.filePath
}

func (s *Streamer) run(ctx context.Context) {
	defer close(s.done)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		// Fall back to ticker-only mode
		s.runTickerOnly(ctx)
		return
	}
	defer watcher.Close()

	var watchedFile string
	ticker := time.NewTicker(tickerFallback)
	defer ticker.Stop()

	batchTimer := time.NewTicker(batchInterval)
	defer batchTimer.Stop()

	var pending []string

	flush := func() {
		if len(pending) > 0 {
			batch := make([]string, len(pending))
			copy(batch, pending)
			pending = pending[:0]
			select {
			case s.Lines <- batch:
			case <-ctx.Done():
			}
		}
	}

	readNew := func() {
		s.mu.Lock()
		path := s.filePath
		s.mu.Unlock()

		if path == "" {
			return
		}

		// Update watcher if file changed
		if path != watchedFile {
			if watchedFile != "" {
				_ = watcher.Remove(watchedFile)
			}
			_ = watcher.Add(path)
			watchedFile = path
		}

		lines := s.readNewLines(path)
		if len(lines) > 0 {
			pending = append(pending, lines...)
		}
	}

	for {
		select {
		case <-ctx.Done():
			flush()
			return

		case <-watcher.Events:
			readNew()

		case <-ticker.C:
			readNew()

		case <-batchTimer.C:
			flush()
		}
	}
}

func (s *Streamer) runTickerOnly(ctx context.Context) {
	ticker := time.NewTicker(tickerFallback)
	defer ticker.Stop()

	batchTimer := time.NewTicker(batchInterval)
	defer batchTimer.Stop()

	var pending []string

	for {
		select {
		case <-ctx.Done():
			if len(pending) > 0 {
				select {
				case s.Lines <- pending:
				default:
				}
			}
			return

		case <-ticker.C:
			s.mu.Lock()
			path := s.filePath
			s.mu.Unlock()

			if path == "" {
				continue
			}
			lines := s.readNewLines(path)
			if len(lines) > 0 {
				pending = append(pending, lines...)
			}

		case <-batchTimer.C:
			if len(pending) > 0 {
				batch := make([]string, len(pending))
				copy(batch, pending)
				pending = pending[:0]
				select {
				case s.Lines <- batch:
				case <-ctx.Done():
					return
				}
			}
		}
	}
}

func (s *Streamer) readNewLines(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	// Check file size for truncation recovery
	info, err := f.Stat()
	if err != nil {
		return nil
	}

	s.mu.Lock()
	if info.Size() < s.offset {
		// File was truncated, reset
		s.offset = 0
	}
	offset := s.offset
	s.mu.Unlock()

	if info.Size() == offset {
		return nil
	}

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil
	}

	var lines []string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024) // 2MB max for large tool results
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	newOffset, err := f.Seek(0, io.SeekCurrent)
	if err != nil {
		newOffset = info.Size()
	}

	s.mu.Lock()
	s.offset = newOffset
	s.mu.Unlock()

	return lines
}
