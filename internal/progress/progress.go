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
