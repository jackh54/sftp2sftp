package transfer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/jackh54/sftp2sftp/internal/manifest"
	"github.com/jackh54/sftp2sftp/internal/progress"
	"github.com/jackh54/sftp2sftp/internal/sftpclient"
	"github.com/jackh54/sftp2sftp/internal/state"
	"github.com/jackh54/sftp2sftp/internal/verify"
	"github.com/pkg/sftp"
)

const (
	pipeThreshold = 512 * 1024
	maxRetries    = 3
)

type Options struct {
	Concurrency int
	Resume      bool
	StatePath   string
	Verify      verify.Mode
}

type Runner struct {
	manager  *sftpclient.Manager
	manifest manifest.Manifest
	state    *state.File
	opts     Options
	tracker  *progress.Tracker
}

type worker struct {
	id       int
	dirCache map[string]struct{}
	dirMu    sync.Mutex
}

func New(manager *sftpclient.Manager, m manifest.Manifest, st *state.File, opts Options) *Runner {
	if opts.Concurrency <= 0 {
		opts.Concurrency = 8
	}
	return &Runner{
		manager:  manager,
		manifest: m,
		state:    st,
		opts:     opts,
		tracker:  progress.New(m.TotalBytes),
	}
}

func (r *Runner) Run(ctx context.Context) error {
	if len(r.manifest.Files) == 0 {
		fmt.Fprintln(os.Stderr, "nothing to transfer")
		return nil
	}

	reporter := progress.NewReporter(r.tracker, 200*time.Millisecond)
	reporter.Start()
	defer reporter.Stop()

	jobs := make(chan manifest.File)
	errCh := make(chan error, r.opts.Concurrency)
	var wg sync.WaitGroup

	for i := 0; i < r.opts.Concurrency; i++ {
		w := &worker{id: i, dirCache: map[string]struct{}{}}
		wg.Add(1)
		go func(w *worker) {
			defer wg.Done()
			for file := range jobs {
				if err := r.transferWithRetry(ctx, w, file); err != nil {
					errCh <- err
					return
				}
			}
		}(w)
	}

	var sendErr error
loop:
	for _, file := range r.manifest.Files {
		select {
		case <-ctx.Done():
			sendErr = ctx.Err()
			break loop
		case err := <-errCh:
			sendErr = err
			break loop
		default:
		}

		select {
		case <-ctx.Done():
			sendErr = ctx.Err()
			break loop
		case err := <-errCh:
			sendErr = err
			break loop
		case jobs <- file:
		}
	}
	close(jobs)
	wg.Wait()

	select {
	case err := <-errCh:
		if sendErr == nil {
			sendErr = err
		}
	default:
	}

	if sendErr != nil {
		return sendErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	if r.opts.Resume && r.opts.StatePath != "" {
		if err := r.state.Save(r.opts.StatePath); err != nil {
			return fmt.Errorf("save state: %w", err)
		}
	}

	return nil
}

func (r *Runner) transferWithRetry(ctx context.Context, w *worker, file manifest.File) error {
	var last error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := r.manager.EnsureReconnect(ctx, func() error {
			return r.transferFile(ctx, w, file)
		})
		if err == nil {
			return nil
		}
		last = err
		if attempt < maxRetries {
			backoff := time.Duration(attempt*attempt) * time.Second
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}
		}
	}
	return fmt.Errorf("transfer %s failed after %d attempts: %w", file.RelPath, maxRetries, last)
}

func (r *Runner) transferFile(ctx context.Context, w *worker, file manifest.File) error {
	srcPath := r.manifest.SourcePath(file.RelPath)
	dstPath := r.manifest.DestPath(file.RelPath)

	r.tracker.StartFile(file.RelPath)
	defer r.tracker.FinishFile(file.RelPath)

	source := r.manager.Source.Client(w.id)
	dest := r.manager.Dest.Client(w.id)

	return source.WithSFTP(func(srcSFTP *sftp.Client) error {
		return dest.WithSFTP(func(dstSFTP *sftp.Client) error {
			if err := ensureFreshDest(dstSFTP, dstPath, file.Size); err != nil {
				return err
			}

			if err := w.ensureDir(dstSFTP, path.Dir(dstPath)); err != nil {
				return err
			}

			srcFile, err := srcSFTP.Open(srcPath)
			if err != nil {
				return fmt.Errorf("open source %s: %w", srcPath, err)
			}
			defer srcFile.Close()

			dstFile, err := dstSFTP.Create(dstPath)
			if err != nil {
				return fmt.Errorf("create dest %s: %w", dstPath, err)
			}
			defer dstFile.Close()

			written, err := streamFile(dstFile, srcFile, file.Size, r.tracker)
			if err != nil {
				return fmt.Errorf("stream %s: %w", file.RelPath, err)
			}
			if written != file.Size {
				return fmt.Errorf("short write for %s: got %d want %d", file.RelPath, written, file.Size)
			}

			if err := dstSFTP.Chmod(dstPath, file.Mode); err != nil {
				_ = err
			}

			var md5sum string
			if r.opts.Verify == verify.ModeMD5 {
				srcHash, err := verify.HashRemote(srcSFTP, srcPath)
				if err != nil {
					return err
				}
				dstHash, err := verify.HashRemote(dstSFTP, dstPath)
				if err != nil {
					return err
				}
				if srcHash != dstHash {
					return fmt.Errorf("md5 mismatch for %s", file.RelPath)
				}
				md5sum = srcHash
			} else if r.opts.Verify == verify.ModeSize {
				if err := verify.Verify(dstSFTP, dstPath, file.Size, "", verify.ModeSize); err != nil {
					return err
				}
			}

			if r.opts.Resume {
				r.state.MarkComplete(file.RelPath, file.Size, md5sum)
				if r.opts.StatePath != "" {
					_ = r.state.Save(r.opts.StatePath)
				}
			}

			return nil
		})
	})
}

func (w *worker) ensureDir(client *sftp.Client, dir string) error {
	if dir == "" || dir == "/" || dir == "." {
		return nil
	}

	w.dirMu.Lock()
	if _, ok := w.dirCache[dir]; ok {
		w.dirMu.Unlock()
		return nil
	}
	w.dirMu.Unlock()

	if err := sftpclient.MkdirAll(client, dir); err != nil {
		return err
	}

	w.dirMu.Lock()
	w.dirCache[dir] = struct{}{}
	w.dirMu.Unlock()
	return nil
}

// streamFile copies src to dst, overlapping concurrent SFTP reads and writes when useful.
func streamFile(dst *sftp.File, src *sftp.File, size int64, tracker *progress.Tracker) (int64, error) {
	if size < pipeThreshold {
		return src.WriteTo(&countingWriter{w: dst, t: tracker})
	}

	pr, pw := io.Pipe()
	errCh := make(chan error, 1)
	go func() {
		_, err := src.WriteTo(pw)
		_ = pw.CloseWithError(err)
		errCh <- err
	}()

	written, copyErr := io.Copy(&countingWriter{w: dst, t: tracker}, pr)
	pipeErr := <-errCh
	if copyErr != nil {
		return written, copyErr
	}
	return written, pipeErr
}

type countingWriter struct {
	w io.Writer
	t *progress.Tracker
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	n, err := cw.w.Write(p)
	if n > 0 && cw.t != nil {
		cw.t.AddBytes(int64(n))
	}
	return n, err
}

func ensureFreshDest(client *sftp.Client, dstPath string, expectedSize int64) error {
	st, err := client.Stat(dstPath)
	if err != nil {
		if isNotExist(err) {
			return nil
		}
		return fmt.Errorf("stat dest %s: %w", dstPath, err)
	}

	if st.Size() == expectedSize {
		return nil
	}

	if err := client.Remove(dstPath); err != nil {
		return fmt.Errorf("remove partial %s: %w", dstPath, err)
	}
	return nil
}

func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "does not exist") || os.IsNotExist(err)
}
