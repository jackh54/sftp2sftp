package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/jackh54/sftp2sftp/internal/auth"
	"github.com/jackh54/sftp2sftp/internal/endpoint"
	"github.com/jackh54/sftp2sftp/internal/exclude"
	"github.com/jackh54/sftp2sftp/internal/sftpclient"
	"github.com/jackh54/sftp2sftp/internal/state"
	"github.com/jackh54/sftp2sftp/internal/transfer"
	"github.com/jackh54/sftp2sftp/internal/verify"
	"github.com/jackh54/sftp2sftp/internal/walker"
)

type Config struct {
	Source       string
	Dest         string
	SourceKey    string
	DestKey      string
	Exclude      []string
	Concurrency  int
	Resume       bool
	NoMCDefaults bool
	ChunkSize    int
	Verify       verify.Mode
	StatePath    string
}

func ParseArgs(args []string) (Config, error) {
	fs := flag.NewFlagSet("mctransfer", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: mctransfer --source user@host:port/path --dest user@host:port/path [options]\n\n")
		fmt.Fprintf(os.Stderr, "Direct SFTP-to-SFTP transfer without writing files to local disk.\n\n")
		fs.PrintDefaults()
	}

	var cfg Config
	var excludeRaw string
	var verifyRaw string

	fs.StringVar(&cfg.Source, "source", "", "Source SFTP endpoint: user@host:port/path")
	fs.StringVar(&cfg.Dest, "dest", "", "Destination SFTP endpoint: user@host:port/path")
	fs.StringVar(&cfg.SourceKey, "source-key", "", "SSH private key for source")
	fs.StringVar(&cfg.DestKey, "dest-key", "", "SSH private key for destination")
	fs.StringVar(&excludeRaw, "exclude", "", "Comma-separated exclude patterns")
	fs.IntVar(&cfg.Concurrency, "concurrency", 4, "Parallel file transfers")
	fs.BoolVar(&cfg.Resume, "resume", false, "Resume from .mctransfer-state.json")
	fs.BoolVar(&cfg.NoMCDefaults, "no-mc-defaults", false, "Disable default Minecraft server excludes")
	fs.IntVar(&cfg.ChunkSize, "chunk-size", 64*1024, "Stream buffer size in bytes")
	fs.StringVar(&verifyRaw, "verify", "", "Post-transfer verification: size or md5")
	fs.StringVar(&cfg.StatePath, "state-file", state.DefaultFile, "Resume state file path")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	if cfg.Source == "" || cfg.Dest == "" {
		return Config{}, fmt.Errorf("--source and --dest are required")
	}

	if excludeRaw != "" {
		for _, part := range strings.Split(excludeRaw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				cfg.Exclude = append(cfg.Exclude, part)
			}
		}
	}

	if verifyRaw != "" {
		mode, err := verify.ParseMode(verifyRaw)
		if err != nil {
			return Config{}, err
		}
		cfg.Verify = mode
	}

	return cfg, nil
}

func Run(ctx context.Context, cfg Config) error {
	srcEP, err := endpoint.Parse(cfg.Source)
	if err != nil {
		return err
	}
	dstEP, err := endpoint.Parse(cfg.Dest)
	if err != nil {
		return err
	}

	sourceAuth, err := resolveAuth("source", cfg.SourceKey)
	if err != nil {
		return err
	}
	destAuth, err := resolveAuth("dest", cfg.DestKey)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "connecting to source %s ...\n", srcEP.Addr())
	sourceClient, err := sftpclient.Connect(ctx, "source", srcEP, sourceAuth)
	if err != nil {
		return err
	}
	defer sourceClient.Close()

	fmt.Fprintf(os.Stderr, "connecting to dest %s ...\n", dstEP.Addr())
	destClient, err := sftpclient.Connect(ctx, "dest", dstEP, destAuth)
	if err != nil {
		return err
	}
	defer destClient.Close()

	patterns := append([]string{}, cfg.Exclude...)
	if !cfg.NoMCDefaults {
		patterns = append(patterns, exclude.DefaultMC...)
	}
	matcher := exclude.New(patterns...)

	fmt.Fprintln(os.Stderr, "building manifest from source ...")
	m, err := walker.Build(ctx, sourceClient, srcEP.Path, matcher)
	if err != nil {
		return err
	}
	m.DestRoot = dstEP.Path

	fmt.Fprintf(os.Stderr, "found %d files (%s total)\n", len(m.Files), humanBytes(m.TotalBytes))

	st := &state.File{Source: cfg.Source, Dest: cfg.Dest, Done: map[string]state.Entry{}}
	if cfg.Resume {
		loaded, err := state.Load(cfg.StatePath)
		if err != nil {
			return err
		}
		if loaded.Source != "" && loaded.Source != cfg.Source {
			return fmt.Errorf("state file source mismatch: %q vs %q", loaded.Source, cfg.Source)
		}
		if loaded.Dest != "" && loaded.Dest != cfg.Dest {
			return fmt.Errorf("state file dest mismatch: %q vs %q", loaded.Dest, cfg.Dest)
		}
		st = loaded
		st.Source = cfg.Source
		st.Dest = cfg.Dest
		before := len(m.Files)
		m = st.Filter(m)
		fmt.Fprintf(os.Stderr, "resume: skipping %d files, %d remaining (%s)\n",
			before-len(m.Files), len(m.Files), humanBytes(m.TotalBytes))
	}

	manager := sftpclient.NewManager(sourceClient, destClient)
	runner := transfer.New(manager, m, st, transfer.Options{
		Concurrency: cfg.Concurrency,
		ChunkSize:   cfg.ChunkSize,
		Resume:      cfg.Resume,
		StatePath:   cfg.StatePath,
		Verify:      cfg.Verify,
	})

	if err := runner.Run(ctx); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "transfer complete")
	return nil
}

func resolveAuth(label, keyPath string) (auth.Method, error) {
	method := auth.Method{KeyPath: keyPath}
	if method.KeyPath == "" {
		for _, candidate := range defaultKeyCandidates() {
			if _, err := os.Stat(candidate); err == nil {
				method.KeyPath = candidate
				break
			}
		}
	}

	if method.KeyPath != "" {
		if _, err := method.Signers(); err == nil {
			return method, nil
		}
	}

	pw, err := auth.PromptPassword(fmt.Sprintf("%s password", label))
	if err != nil {
		return auth.Method{}, err
	}
	method.Password = pw
	return method, nil
}

func defaultKeyCandidates() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	sshDir := strings.TrimRight(home, string(os.PathSeparator)) + string(os.PathSeparator) + ".ssh"
	return []string{
		sshDir + string(os.PathSeparator) + "id_ed25519",
		sshDir + string(os.PathSeparator) + "id_rsa",
	}
}

func humanBytes(n int64) string {
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
