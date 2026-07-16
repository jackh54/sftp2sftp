package walker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/jackh54/sftp2sftp/internal/exclude"
	"github.com/jackh54/sftp2sftp/internal/manifest"
	"github.com/jackh54/sftp2sftp/internal/sftpclient"
	"github.com/pkg/sftp"
)

// Build walks the source tree and returns a manifest of files to transfer.
func Build(ctx context.Context, client *sftpclient.Client, root string, matcher *exclude.Matcher) (manifest.Manifest, error) {
	root = strings.TrimRight(root, "/")
	if root == "" {
		root = "/"
	}

	m := manifest.Manifest{
		SourceRoot: root,
		Files:      nil,
	}

	err := client.WithSFTP(func(s *sftp.Client) error {
		return walk(ctx, s, root, root, matcher, &m)
	})
	if err != nil {
		return manifest.Manifest{}, err
	}

	for _, f := range m.Files {
		m.TotalBytes += f.Size
	}
	return m, nil
}

func walk(ctx context.Context, client *sftp.Client, root, current string, matcher *exclude.Matcher, m *manifest.Manifest) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

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
		rel := strings.TrimPrefix(full, root)
		rel = strings.TrimPrefix(rel, "/")

		if matcher != nil && matcher.Match(rel) {
			continue
		}

		mode := entry.Mode()
		switch {
		case mode&os.ModeSymlink != 0:
			// Skip symlinks; streaming copy cannot preserve link semantics safely.
			continue
		case mode.IsDir():
			if err := walk(ctx, client, root, full, matcher, m); err != nil {
				return err
			}
		case mode.IsRegular():
			m.Files = append(m.Files, manifest.File{
				RelPath: rel,
				Size:    entry.Size(),
				Mode:    mode,
			})
		default:
			// Skip sockets, devices, etc.
			continue
		}
	}

	return nil
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
