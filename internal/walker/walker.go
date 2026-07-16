package walker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jackh54/sftp2sftp/internal/exclude"
	"github.com/jackh54/sftp2sftp/internal/manifest"
	"github.com/jackh54/sftp2sftp/internal/progress"
	"github.com/jackh54/sftp2sftp/internal/sftpclient"
	"github.com/pkg/sftp"
)

// Build walks the source tree and returns a manifest of files to transfer.
// workers controls parallel directory scans; 1 is fully sequential.
func Build(ctx context.Context, client *sftpclient.Client, root string, matcher *exclude.Matcher, workers int) (manifest.Manifest, error) {
	root = strings.TrimRight(root, "/")
	if root == "" {
		root = "/"
	}

	m := manifest.Manifest{
		SourceRoot: root,
		Files:      nil,
	}

	scan := progress.NewScanTracker()
	reporter := progress.NewScanReporter(scan, 500*time.Millisecond)
	reporter.Start()
	defer reporter.Stop()

	err := client.WithSFTP(func(s *sftp.Client) error {
		if workers <= 1 {
			return walk(ctx, s, root, root, matcher, &m, scan)
		}
		return walkParallel(ctx, s, root, matcher, &m, scan, workers)
	})
	if err != nil {
		return manifest.Manifest{}, err
	}

	for _, f := range m.Files {
		m.TotalBytes += f.Size
	}
	return m, nil
}

func walk(ctx context.Context, client *sftp.Client, root, current string, matcher *exclude.Matcher, m *manifest.Manifest, scan *progress.ScanTracker) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	scan.VisitDir(current)

	entries, err := client.ReadDir(current)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", current, err)
	}

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		full := path.Join(current, entry.Name())
		rel := relPath(root, full)

		if matcher != nil && matcher.Match(rel) {
			continue
		}

		mode := entry.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			continue
		case mode.IsDir():
			if err := walk(ctx, client, root, full, matcher, m, scan); err != nil {
				return err
			}
		case mode.IsRegular():
			size := entry.Size()
			m.Files = append(m.Files, manifest.File{
				RelPath: rel,
				Size:    size,
				Mode:    mode,
			})
			scan.AddFile(size)
		default:
			continue
		}
	}

	return nil
}

func walkParallel(ctx context.Context, client *sftp.Client, root string, matcher *exclude.Matcher, m *manifest.Manifest, scan *progress.ScanTracker, workers int) error {
	dirs := make(chan string, workers*8)
	var pending sync.WaitGroup
	var filesMu sync.Mutex
	var firstErr error
	var errOnce sync.Once

	setErr := func(err error) {
		if err != nil {
			errOnce.Do(func() { firstErr = err })
		}
	}

	enqueue := func(dir string) {
		pending.Add(1)
		select {
		case dirs <- dir:
		case <-ctx.Done():
			pending.Done()
			setErr(ctx.Err())
		}
	}

	var workerWg sync.WaitGroup
	for i := 0; i < workers; i++ {
		workerWg.Add(1)
		go func() {
			defer workerWg.Done()
			for dir := range dirs {
				if err := scanDir(ctx, client, root, dir, matcher, m, &filesMu, scan, enqueue); err != nil {
					setErr(err)
				}
				pending.Done()
			}
		}()
	}

	enqueue(root)
	go func() {
		pending.Wait()
		close(dirs)
	}()

	workerWg.Wait()
	return firstErr
}

func scanDir(ctx context.Context, client *sftp.Client, root, current string, matcher *exclude.Matcher, m *manifest.Manifest, filesMu *sync.Mutex, scan *progress.ScanTracker, enqueue func(string)) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	scan.VisitDir(current)

	entries, err := client.ReadDir(current)
	if err != nil {
		return fmt.Errorf("readdir %s: %w", current, err)
	}

	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		full := path.Join(current, entry.Name())
		rel := relPath(root, full)

		if matcher != nil && matcher.Match(rel) {
			continue
		}

		mode := entry.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			continue
		case mode.IsDir():
			enqueue(full)
		case mode.IsRegular():
			size := entry.Size()
			file := manifest.File{
				RelPath: rel,
				Size:    size,
				Mode:    mode,
			}
			filesMu.Lock()
			m.Files = append(m.Files, file)
			filesMu.Unlock()
			scan.AddFile(size)
		default:
			continue
		}
	}

	return nil
}

func relPath(root, full string) string {
	rel := strings.TrimPrefix(full, root)
	return strings.TrimPrefix(rel, "/")
}

// CountFiles returns the number of files under root without building a full manifest.
func CountFiles(client *sftp.Client, root string) (int, error) {
	n := 0
	var count func(string) error
	count = func(dir string) error {
		entries, err := client.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			full := path.Join(dir, entry.Name())
			mode := entry.Mode()
			switch {
			case mode.IsDir():
				if err := count(full); err != nil {
					return err
				}
			case mode.IsRegular():
				n++
			}
		}
		return nil
	}
	if err := count(root); err != nil {
		return 0, err
	}
	return n, nil
}

// DrainReader discards a reader (used in tests).
func DrainReader(r io.Reader) error {
	_, err := io.Copy(io.Discard, r)
	return err
}
