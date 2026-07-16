package state

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/jackh54/sftp2sftp/internal/manifest"
)

const DefaultFile = ".sftp2sftp-state.json"

type Entry struct {
	Size        int64     `json:"size"`
	CompletedAt time.Time `json:"completed_at"`
	MD5         string    `json:"md5,omitempty"`
}

type File struct {
	Source string           `json:"source"`
	Dest   string           `json:"dest"`
	Done   map[string]Entry `json:"completed"`
}

func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &File{Done: map[string]Entry{}}, nil
		}
		return nil, fmt.Errorf("read state %s: %w", path, err)
	}

	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", path, err)
	}
	if f.Done == nil {
		f.Done = map[string]Entry{}
	}
	return &f, nil
}

func (f *File) Save(path string) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

func (f *File) IsComplete(rel string, size int64) bool {
	entry, ok := f.Done[rel]
	return ok && entry.Size == size
}

func (f *File) MarkComplete(rel string, size int64, md5 string) {
	f.Done[rel] = Entry{
		Size:        size,
		CompletedAt: time.Now().UTC(),
		MD5:         md5,
	}
}

func (f *File) Filter(m manifest.Manifest) manifest.Manifest {
	if len(f.Done) == 0 {
		return m
	}

	out := m
	out.Files = nil
	out.TotalBytes = 0
	for _, file := range m.Files {
		if f.IsComplete(file.RelPath, file.Size) {
			continue
		}
		out.Files = append(out.Files, file)
		out.TotalBytes += file.Size
	}
	return out
}
