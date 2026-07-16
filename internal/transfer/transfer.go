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
	defaultChunkSize = 64 * 1024
	maxRetries       = 3
)

type Options struct {
	Concurrency int
	ChunkSize   int
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

func New(manager *sftpclient.Manager, m manifest.Manifest, st *state.File, opts Options) *Runner {
	if opts.ChunkSize <= 0 {
		opts.ChunkSize = defaultChunkSize
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 4
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			for file := range jobs {
				if err := r.transferWithRetry(ctx, file); err != nil {
					errCh <- err
					return
				}
			}
		}()
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

func (r *Runner) transferWithRetry(ctx context.Context, file manifest.File) error {
	var last error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		err := r.manager.EnsureReconnect(ctx, func() error {
			return r.transferFile(ctx, file)
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

func (r *Runner) transferFile(ctx context.Context, file manifest.File) error {
	srcPath := r.manifest.SourcePath(file.RelPath)
	dstPath := r.manifest.DestPath(file.RelPath)

	r.tracker.StartFile(file.RelPath)
	defer r.tracker.FinishFile(file.RelPath)

	return r.manager.Source.WithSFTP(func(srcSFTP *sftp.Client) error {
		return r.manager.Dest.WithSFTP(func(dstSFTP *sftp.Client) error {
			if err := ensureFreshDest(dstSFTP, dstPath, file.Size); err != nil {
				return err
			}

			if err := sftpclient.MkdirAll(dstSFTP, path.Dir(dstPath)); err != nil {
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

			buf := make([]byte, r.opts.ChunkSize)
			reader := progress.CountingReader{R: srcFile, T: r.tracker}

			written, err := io.CopyBuffer(dstFile, reader, buf)
			if err != nil {
				return fmt.Errorf("stream %s: %w", file.RelPath, err)
			}
			if written != file.Size {
				return fmt.Errorf("short write for %s: got %d want %d", file.RelPath, written, file.Size)
			}

			if err := dstSFTP.Chmod(dstPath, file.Mode); err != nil {
				// Non-fatal on hosts that ignore mode.
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
