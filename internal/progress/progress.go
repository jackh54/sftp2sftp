package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Tracker aggregates byte progress across concurrent transfers.
type Tracker struct {
	totalBytes int64
	doneBytes  atomic.Int64
	inFlight   atomic.Int64

	mu          sync.Mutex
	activeFiles map[string]struct{}

	start time.Time
}

func New(totalBytes int64) *Tracker {
	return &Tracker{
		totalBytes:  totalBytes,
		activeFiles: map[string]struct{}{},
		start:       time.Now(),
	}
}

func (t *Tracker) AddBytes(n int64) {
	if n > 0 {
		t.doneBytes.Add(n)
	}
}

func (t *Tracker) StartFile(path string) {
	t.inFlight.Add(1)
	t.mu.Lock()
	t.activeFiles[path] = struct{}{}
	t.mu.Unlock()
}

func (t *Tracker) FinishFile(path string) {
	t.inFlight.Add(-1)
	t.mu.Lock()
	delete(t.activeFiles, path)
	t.mu.Unlock()
}

func (t *Tracker) DoneBytes() int64 {
	return t.doneBytes.Load()
}

func (t *Tracker) Snapshot() (pct float64, mbps float64, inFlight int, eta time.Duration, active []string) {
	done := t.doneBytes.Load()
	total := t.totalBytes
	if total > 0 {
		pct = float64(done) / float64(total) * 100
	}

	elapsed := time.Since(t.start).Seconds()
	if elapsed > 0 {
		mbps = float64(done) / elapsed / (1024 * 1024)
	}

	inFlight = int(t.inFlight.Load())
	if done > 0 && total > done {
		rate := float64(done) / elapsed
		if rate > 0 {
			eta = time.Duration(float64(total-done)/rate) * time.Second
		}
	}

	t.mu.Lock()
	active = make([]string, 0, len(t.activeFiles))
	for p := range t.activeFiles {
		active = append(active, p)
	}
	t.mu.Unlock()
	return
}

// Reporter redraws a single-line status on stderr.
type Reporter struct {
	tracker  *Tracker
	interval time.Duration
	out      io.Writer
	stop     chan struct{}
	done     chan struct{}
}

func NewReporter(tracker *Tracker, interval time.Duration) *Reporter {
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	return &Reporter{
		tracker:  tracker,
		interval: interval,
		out:      os.Stderr,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (r *Reporter) Start() {
	go func() {
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		defer close(r.done)

		for {
			select {
			case <-r.stop:
				r.render(true)
				return
			case <-ticker.C:
				r.render(false)
			}
		}
	}()
}

func (r *Reporter) Stop() {
	close(r.stop)
	<-r.done
}

func (r *Reporter) render(final bool) {
	pct, mbps, inFlight, eta, active := r.tracker.Snapshot()
	line := fmt.Sprintf("%5.1f%%  %.2f MB/s  in-flight=%d  eta=%s",
		pct, mbps, inFlight, formatETA(eta))
	if len(active) > 0 {
		if len(active) > 2 {
			line += fmt.Sprintf("  [%s, %s, +%d more]", active[0], active[1], len(active)-2)
		} else {
			line += "  [" + strings.Join(active, ", ") + "]"
		}
	}
	if final {
		fmt.Fprintf(r.out, "\r%s\n", padRight(line, 100))
		return
	}
	fmt.Fprintf(r.out, "\r%s", padRight(line, 100))
}

func formatETA(d time.Duration) string {
	if d <= 0 {
		return "--:--"
	}
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%02d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%02d:%02d", m, s)
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

// ScanTracker aggregates progress while walking a remote tree for a manifest.
type ScanTracker struct {
	files atomic.Int64
	dirs  atomic.Int64
	bytes atomic.Int64

	mu         sync.Mutex
	currentDir string

	start time.Time
}

func NewScanTracker() *ScanTracker {
	return &ScanTracker{start: time.Now()}
}

func (t *ScanTracker) VisitDir(dir string) {
	t.dirs.Add(1)
	t.mu.Lock()
	t.currentDir = dir
	t.mu.Unlock()
}

func (t *ScanTracker) AddFile(size int64) {
	t.files.Add(1)
	if size > 0 {
		t.bytes.Add(size)
	}
}

func (t *ScanTracker) Snapshot() (files, dirs, bytes int64, elapsed time.Duration, currentDir string) {
	files = t.files.Load()
	dirs = t.dirs.Load()
	bytes = t.bytes.Load()
	elapsed = time.Since(t.start)
	t.mu.Lock()
	currentDir = t.currentDir
	t.mu.Unlock()
	return
}

// ScanReporter redraws manifest scan progress on stderr.
type ScanReporter struct {
	tracker  *ScanTracker
	interval time.Duration
	out      io.Writer
	stop     chan struct{}
	done     chan struct{}
}

func NewScanReporter(tracker *ScanTracker, interval time.Duration) *ScanReporter {
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	return &ScanReporter{
		tracker:  tracker,
		interval: interval,
		out:      os.Stderr,
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (r *ScanReporter) Start() {
	go func() {
		ticker := time.NewTicker(r.interval)
		defer ticker.Stop()
		defer close(r.done)

		for {
			select {
			case <-r.stop:
				r.render(true)
				return
			case <-ticker.C:
				r.render(false)
			}
		}
	}()
}

func (r *ScanReporter) Stop() {
	close(r.stop)
	<-r.done
}

func (r *ScanReporter) render(final bool) {
	files, dirs, bytes, elapsed, currentDir := r.tracker.Snapshot()
	line := fmt.Sprintf("scanning  %s files  %s  %s dirs  %s",
		formatCount(files), HumanBytes(bytes), formatCount(dirs), formatElapsed(elapsed))
	if currentDir != "" {
		line += "  " + truncatePath(currentDir, 48)
	}
	if final {
		fmt.Fprintf(r.out, "\r%s\n", padRight(line, 100))
		return
	}
	fmt.Fprintf(r.out, "\r%s", padRight(line, 100))
}

func formatCount(n int64) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}

	s := fmt.Sprintf("%d", n)
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func formatElapsed(d time.Duration) string {
	d = d.Round(time.Second)
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	if m > 0 {
		return fmt.Sprintf("%dm%02ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}

func truncatePath(s string, max int) string {
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return "..." + s[len(s)-(max-3):]
}

// HumanBytes formats a byte count for display.
func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// CountingReader wraps a reader and reports bytes to the tracker.
type CountingReader struct {
	R io.Reader
	T *Tracker
}

func (c CountingReader) Read(p []byte) (int, error) {
	n, err := c.R.Read(p)
	if n > 0 {
		c.T.AddBytes(int64(n))
	}
	return n, err
}
