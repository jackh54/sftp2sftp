package manifest

import (
	"os"
	"time"
)

// File describes one file to transfer.
type File struct {
	RelPath string `json:"rel_path"`
	Size    int64  `json:"size"`
	Mode    os.FileMode `json:"mode"`
}

// Manifest is the full transfer plan built from the source tree walk.
type Manifest struct {
	SourceRoot string `json:"source_root"`
	DestRoot   string `json:"dest_root"`
	Files      []File `json:"files"`
	TotalBytes int64  `json:"total_bytes"`
	BuiltAt    time.Time `json:"built_at"`
}

func (m Manifest) SourcePath(rel string) string {
	if m.SourceRoot == "/" {
		return "/" + rel
	}
	return m.SourceRoot + "/" + rel
}

func (m Manifest) DestPath(rel string) string {
	if m.DestRoot == "/" {
		return "/" + rel
	}
	return m.DestRoot + "/" + rel
}
