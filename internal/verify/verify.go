package verify

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"

	"github.com/pkg/sftp"
)

type Mode string

const (
	ModeSize Mode = "size"
	ModeMD5  Mode = "md5"
)

func ParseMode(raw string) (Mode, error) {
	switch Mode(raw) {
	case "", "size":
		return ModeSize, nil
	case "md5":
		return ModeMD5, nil
	default:
		return "", fmt.Errorf("unknown verify mode %q (use size or md5)", raw)
	}
}

func Verify(client *sftp.Client, path string, expectedSize int64, expectedMD5 string, mode Mode) error {
	switch mode {
	case ModeSize:
		return verifySize(client, path, expectedSize)
	case ModeMD5:
		return verifyMD5(client, path, expectedSize, expectedMD5)
	default:
		return fmt.Errorf("unsupported verify mode %q", mode)
	}
}

func verifySize(client *sftp.Client, path string, expected int64) error {
	st, err := client.Stat(path)
	if err != nil {
		return fmt.Errorf("stat dest %s: %w", path, err)
	}
	if st.Size() != expected {
		return fmt.Errorf("size mismatch for %s: got %d want %d", path, st.Size(), expected)
	}
	return nil
}

func verifyMD5(client *sftp.Client, path string, expectedSize int64, expectedMD5 string) error {
	if err := verifySize(client, path, expectedSize); err != nil {
		return err
	}
	if expectedMD5 == "" {
		return fmt.Errorf("missing expected md5 for %s", path)
	}

	got, err := HashRemote(client, path)
	if err != nil {
		return err
	}
	if got != expectedMD5 {
		return fmt.Errorf("md5 mismatch for %s: got %s want %s", path, got, expectedMD5)
	}
	return nil
}

func HashRemote(client *sftp.Client, path string) (string, error) {
	f, err := client.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func HashReader(r io.Reader) (string, error) {
	h := md5.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
