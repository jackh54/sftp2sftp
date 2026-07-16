package browse

import (
	"context"

	"github.com/jackh54/sftp2sftp/internal/exclude"
	"github.com/jackh54/sftp2sftp/internal/manifest"
	"github.com/jackh54/sftp2sftp/internal/sftpclient"
	"github.com/jackh54/sftp2sftp/internal/walker"
)

// ToManifest expands file and directory selections into a transfer manifest.
func ToManifest(ctx context.Context, client *sftpclient.Client, root, destRoot string, selections []Selection, matcher *exclude.Matcher) (manifest.Manifest, error) {
	root = cleanRoot(root)
	m := manifest.Manifest{
		SourceRoot: root,
		DestRoot:   destRoot,
	}

	seen := map[string]struct{}{}
	var files []manifest.File
	var dirs []string

	for _, sel := range selections {
		if sel.IsDir {
			dirs = append(dirs, sel.RelPath)
			continue
		}
		if _, ok := seen[sel.RelPath]; ok {
			continue
		}
		seen[sel.RelPath] = struct{}{}
		files = append(files, sel.File)
	}

	if len(dirs) > 0 {
		sub, err := walker.BuildSelected(ctx, client, root, dirs, matcher)
		if err != nil {
			return manifest.Manifest{}, err
		}
		for _, f := range sub.Files {
			if _, ok := seen[f.RelPath]; ok {
				continue
			}
			seen[f.RelPath] = struct{}{}
			files = append(files, f)
		}
	}

	for _, f := range files {
		m.Files = append(m.Files, f)
		m.TotalBytes += f.Size
	}
	return m, nil
}
