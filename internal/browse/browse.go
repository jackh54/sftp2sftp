package browse

import (
	"context"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/jackh54/sftp2sftp/internal/exclude"
	"github.com/jackh54/sftp2sftp/internal/manifest"
	"github.com/jackh54/sftp2sftp/internal/progress"
	"github.com/jackh54/sftp2sftp/internal/sftpclient"
	"github.com/pkg/sftp"
)

var (
	titleStyle  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	pathStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	helpStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	mutedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	cursorStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	selStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	errStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))
)

// Selection is one user-chosen file or directory (directories expand recursively on confirm).
type Selection struct {
	RelPath string
	IsDir   bool
	File    manifest.File
}

type dirEntry struct {
	name     string
	relPath  string
	isDir    bool
	size     int64
	mode     os.FileMode
	excluded bool
}

type model struct {
	client   *sftpclient.Client
	root     string
	cwd      string
	entries  []dirEntry
	cursor   int
	selected map[string]Selection
	matcher  *exclude.Matcher
	width    int
	height   int
	loading  bool
	err      error
	quitting bool
	done     bool
}

type dirLoadedMsg struct {
	entries []dirEntry
	err     error
}

type errMsg struct {
	err error
}

// Run opens an interactive SFTP browser rooted at root. Returns chosen selections or an error.
func Run(ctx context.Context, client *sftpclient.Client, root string, matcher *exclude.Matcher) ([]Selection, error) {
	root = cleanRoot(root)
	m := model{
		client:   client,
		root:     root,
		cwd:      root,
		selected: map[string]Selection{},
		matcher:  matcher,
		loading:  true,
	}

	p := tea.NewProgram(m, tea.WithContext(ctx))
	final, err := p.Run()
	if err != nil {
		return nil, err
	}

	result := final.(model)
	if result.err != nil {
		return nil, result.err
	}
	if !result.done {
		return nil, fmt.Errorf("browse cancelled")
	}

	out := make([]Selection, 0, len(result.selected))
	for _, sel := range result.selected {
		out = append(out, sel)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RelPath < out[j].RelPath })
	if len(out) == 0 {
		return nil, fmt.Errorf("no files or folders selected")
	}
	return out, nil
}

func (m model) Init() tea.Cmd {
	return m.loadDirCmd()
}

func (m model) loadDirCmd() tea.Cmd {
	cwd := m.cwd
	root := m.root
	matcher := m.matcher
	client := m.client
	return func() tea.Msg {
		entries, err := listDir(client, root, cwd, matcher)
		return dirLoadedMsg{entries: entries, err: err}
	}
}

func listDir(client *sftpclient.Client, root, cwd string, matcher *exclude.Matcher) ([]dirEntry, error) {
	var entries []dirEntry
	err := client.WithSFTP(func(s *sftp.Client) error {
		raw, err := s.ReadDir(cwd)
		if err != nil {
			return fmt.Errorf("readdir %s: %w", cwd, err)
		}

		for _, entry := range raw {
			name := entry.Name()
			if name == "." || name == ".." {
				continue
			}

			full := path.Join(cwd, name)
			rel := relPath(root, full)
			mode := entry.Mode()

			excluded := matcher != nil && matcher.Match(rel)
			switch {
			case mode&os.ModeSymlink != 0:
				continue
			case mode.IsDir():
				entries = append(entries, dirEntry{
					name:     name + "/",
					relPath:  rel,
					isDir:    true,
					mode:     mode,
					excluded: excluded,
				})
			case mode.IsRegular():
				entries = append(entries, dirEntry{
					name:     name,
					relPath:  rel,
					isDir:    false,
					size:     entry.Size(),
					mode:     mode,
					excluded: excluded,
				})
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Slice(entries, func(i, j int) bool {
		if entries[i].isDir != entries[j].isDir {
			return entries[i].isDir
		}
		return entries[i].name < entries[j].name
	})

	if cwd != root {
		parent := dirEntry{name: "../", relPath: "", isDir: true}
		entries = append([]dirEntry{parent}, entries...)
	}
	return entries, nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.loading {
			return m, nil
		}
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			m.err = fmt.Errorf("browse cancelled")
			return m, tea.Quit
		case "c":
			m.done = true
			return m, tea.Quit
		case "up", "k":
			if m.cursor > 0 {
				m.cursor--
			}
		case "down", "j":
			if m.cursor < len(m.entries)-1 {
				m.cursor++
			}
		case "left", "h", "backspace":
			if parent := parentDir(m.cwd, m.root); parent != "" {
				m.cwd = parent
				m.cursor = 0
				m.loading = true
				return m, m.loadDirCmd()
			}
		case "right", "l", "enter":
			if len(m.entries) == 0 {
				return m, nil
			}
			entry := m.entries[m.cursor]
			if entry.name == "../" {
				if parent := parentDir(m.cwd, m.root); parent != "" {
					m.cwd = parent
					m.cursor = 0
					m.loading = true
					return m, m.loadDirCmd()
				}
				return m, nil
			}
			if entry.isDir {
				m.cwd = path.Join(m.cwd, strings.TrimSuffix(entry.name, "/"))
				m.cursor = 0
				m.loading = true
				return m, m.loadDirCmd()
			}
			m.toggle(entry)
		case " ":
			if len(m.entries) == 0 {
				return m, nil
			}
			entry := m.entries[m.cursor]
			if entry.name == "../" {
				return m, nil
			}
			m.toggle(entry)
		}

	case dirLoadedMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			return m, tea.Quit
		}
		m.entries = msg.entries
		if m.cursor >= len(m.entries) {
			m.cursor = max(0, len(m.entries)-1)
		}
		return m, nil

	case errMsg:
		m.err = msg.err
		return m, tea.Quit
	}

	return m, nil
}

func (m model) toggle(entry dirEntry) {
	if entry.excluded {
		return
	}
	if _, ok := m.selected[entry.relPath]; ok {
		delete(m.selected, entry.relPath)
		return
	}
	sel := Selection{RelPath: entry.relPath, IsDir: entry.isDir}
	if !entry.isDir {
		sel.File = manifest.File{
			RelPath: entry.relPath,
			Size:    entry.size,
			Mode:    entry.mode,
		}
	}
	m.selected[entry.relPath] = sel
}

func (m model) View() string {
	if m.quitting && m.err != nil {
		return ""
	}

	var b strings.Builder
	b.WriteString(titleStyle.Render("Select files to copy"))
	b.WriteByte('\n')
	b.WriteString(pathStyle.Render(displayPath(m.cwd, m.root)))
	b.WriteByte('\n')
	b.WriteString(helpStyle.Render("↑/↓ move  enter open  space select  ← up  c confirm  q quit"))
	b.WriteByte('\n')
	b.WriteString(strings.Repeat("─", max(40, m.width)))
	b.WriteByte('\n')

	if m.loading {
		b.WriteString(helpStyle.Render("loading..."))
		b.WriteByte('\n')
	} else if len(m.entries) == 0 {
		b.WriteString(mutedStyle.Render("(empty directory)"))
		b.WriteByte('\n')
	} else {
		visible := m.entries
		start := 0
		maxRows := m.height - 8
		if maxRows < 5 {
			maxRows = 5
		}
		if m.cursor >= maxRows {
			start = m.cursor - maxRows + 1
		}
		end := min(len(visible), start+maxRows)
		for i := start; i < end; i++ {
			entry := visible[i]
			line := m.renderEntry(entry, i == m.cursor)
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}

	b.WriteString(strings.Repeat("─", max(40, m.width)))
	b.WriteByte('\n')
	files, dirs := countSelections(m.selected)
	b.WriteString(helpStyle.Render(fmt.Sprintf("%d files, %d folders selected", files, dirs)))
	return b.String()
}

func (m model) renderEntry(entry dirEntry, active bool) string {
	prefix := "  "
	if active {
		prefix = cursorStyle.Render("> ")
	}

	if entry.name == "../" {
		return prefix + pathStyle.Render("../")
	}

	mark := "[ ]"
	if _, ok := m.selected[entry.relPath]; ok {
		mark = selStyle.Render("[x]")
	}

	name := entry.name
	if entry.excluded {
		name = mutedStyle.Render(name + " (excluded)")
	} else if entry.isDir {
		name = pathStyle.Render(name)
	} else {
		name = fmt.Sprintf("%s  %s", name, mutedStyle.Render(progress.HumanBytes(entry.size)))
	}

	return prefix + mark + " " + name
}

func countSelections(selected map[string]Selection) (files, dirs int) {
	for _, sel := range selected {
		if sel.IsDir {
			dirs++
		} else {
			files++
		}
	}
	return
}

func cleanRoot(root string) string {
	root = strings.TrimRight(root, "/")
	if root == "" {
		return "/"
	}
	return root
}

func parentDir(cwd, root string) string {
	if cwd == root {
		return ""
	}
	parent := path.Dir(cwd)
	if parent == "." || len(parent) < len(root) {
		return root
	}
	if !strings.HasPrefix(parent, root) {
		return root
	}
	return parent
}

func relPath(root, full string) string {
	rel := strings.TrimPrefix(full, root)
	return strings.TrimPrefix(rel, "/")
}

func displayPath(cwd, root string) string {
	if cwd == root {
		if root == "/" {
			return "/"
		}
		return root + "/"
	}
	return cwd
}
