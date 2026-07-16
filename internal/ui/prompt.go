package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackh54/sftp2sftp/internal/cli"
	"github.com/jackh54/sftp2sftp/internal/endpoint"
	"github.com/jackh54/sftp2sftp/internal/state"
	"github.com/jackh54/sftp2sftp/internal/verify"
	"golang.org/x/term"
)

var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("39")).
			MarginBottom(1)
	subtitleStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			MarginBottom(2)
	sectionStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("252")).
			MarginTop(1)
)

// Prompt walks the user through an interactive setup flow.
func Prompt() (cli.Config, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return cli.Config{}, fmt.Errorf("sftp2sftp needs an interactive terminal (stdin is not a TTY)")
	}

	printBanner()

	var (
		source       string
		dest         string
		sourceKey    string
		destKey      string
		excludeRaw   string
		concurrency  string = "4"
		resume       bool
		useMCDefault bool   = true
		verifyChoice string = "none"
	)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("Connection").
				Description("Where to copy from and where to copy to.\nFormat: user@host:port/path"),
			huh.NewInput().
				Title("Source").
				Placeholder("user@host1:22/home/mc/server").
				Value(&source).
				Validate(validateEndpoint),
			huh.NewInput().
				Title("Destination").
				Placeholder("user@host2:22/home/mc/server").
				Value(&dest).
				Validate(validateEndpoint),
		),
		huh.NewGroup(
			huh.NewNote().
				Title("Authentication").
				Description("Leave blank to try ~/.ssh/id_ed25519 and id_rsa, then prompt for password."),
			huh.NewInput().
				Title("Source SSH key").
				Placeholder("optional path to private key").
				Value(&sourceKey),
			huh.NewInput().
				Title("Destination SSH key").
				Placeholder("optional path to private key").
				Value(&destKey),
		),
		huh.NewGroup(
			huh.NewNote().
				Title("Transfer").
				Description("Files stream source → destination in memory. Nothing is written to local disk."),
			huh.NewSelect[string]().
				Title("Parallel transfers").
				Options(
					huh.NewOption("1 (slow, gentle on SSH)", "1"),
					huh.NewOption("2", "2"),
					huh.NewOption("4 (recommended)", "4"),
					huh.NewOption("8", "8"),
					huh.NewOption("16 (aggressive)", "16"),
				).
				Value(&concurrency),
			huh.NewConfirm().
				Title("Resume previous run?").
				Description("Uses .sftp2sftp-state.json to skip completed files.").
				Value(&resume),
			huh.NewSelect[string]().
				Title("Verify after transfer").
				Options(
					huh.NewOption("None", "none"),
					huh.NewOption("File size", "size"),
					huh.NewOption("MD5 checksum (slower)", "md5"),
				).
				Value(&verifyChoice),
		),
		huh.NewGroup(
			huh.NewNote().
				Title("Excludes").
				Description("Skip paths that do not need to be copied."),
			huh.NewConfirm().
				Title("Use Minecraft server defaults?").
				Description("Skips session.lock, cache/, logs/, and *.log").
				Value(&useMCDefault),
			huh.NewInput().
				Title("Extra exclude patterns").
				Placeholder("cache/,logs/,*.tmp (comma-separated, optional)").
				Value(&excludeRaw),
		),
		huh.NewGroup(
			huh.NewNote().
				Title("Ready").
				DescriptionFunc(func() string {
					return summarize(source, dest, concurrency, resume, verifyChoice, useMCDefault, excludeRaw)
				}, &source),
		),
	).
		WithTheme(huh.ThemeBase16()).
		WithShowHelp(true)

	if err := form.Run(); err != nil {
		return cli.Config{}, err
	}

	parallel, err := strconv.Atoi(concurrency)
	if err != nil || parallel < 1 {
		return cli.Config{}, fmt.Errorf("invalid concurrency %q", concurrency)
	}

	cfg := cli.Config{
		Source:       strings.TrimSpace(source),
		Dest:         strings.TrimSpace(dest),
		SourceKey:    strings.TrimSpace(sourceKey),
		DestKey:      strings.TrimSpace(destKey),
		Concurrency:  parallel,
		Resume:       resume,
		NoMCDefaults: !useMCDefault,
		ChunkSize:    64 * 1024,
		StatePath:    state.DefaultFile,
	}

	if excludeRaw != "" {
		for _, part := range strings.Split(excludeRaw, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				cfg.Exclude = append(cfg.Exclude, part)
			}
		}
	}

	if verifyChoice != "none" {
		mode, err := verify.ParseMode(verifyChoice)
		if err != nil {
			return cli.Config{}, err
		}
		cfg.Verify = mode
	}

	return cfg, nil
}

func printBanner() {
	fmt.Println(titleStyle.Render("sftp2sftp"))
	fmt.Println(subtitleStyle.Render("Direct SFTP-to-SFTP transfer — streams in memory, never touches local disk."))
	fmt.Println(sectionStyle.Render("Setup"))
}

func validateEndpoint(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("required")
	}
	if _, err := endpoint.Parse(s); err != nil {
		return err
	}
	return nil
}

func summarize(source, dest, concurrency string, resume bool, verify string, mcDefaults bool, excludes string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "  %s\n    → %s\n\n", source, dest)
	fmt.Fprintf(&b, "  parallel: %s\n", concurrency)
	if resume {
		b.WriteString("  resume: yes\n")
	}
	if verify != "none" {
		fmt.Fprintf(&b, "  verify: %s\n", verify)
	}
	if mcDefaults {
		b.WriteString("  mc defaults: on\n")
	}
	if strings.TrimSpace(excludes) != "" {
		fmt.Fprintf(&b, "  extra excludes: %s\n", excludes)
	}
	b.WriteString("\nPress enter on the last step to start.")
	return b.String()
}
