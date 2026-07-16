package exclude

import (
	"path"
	"strings"
)

var DefaultMC = []string{
	"session.lock",
	"cache/",
	"logs/",
	"*.log",
}

// Matcher decides whether a relative remote path should be skipped.
type Matcher struct {
	patterns []string
}

func New(patterns ...string) *Matcher {
	return &Matcher{patterns: patterns}
}

func (m *Matcher) Match(relPath string) bool {
	relPath = strings.TrimPrefix(relPath, "/")
	if relPath == "" {
		return false
	}

	base := path.Base(relPath)
	for _, pattern := range m.patterns {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		if matchPattern(relPath, base, pattern) {
			return true
		}
	}
	return false
}

func matchPattern(relPath, base, pattern string) bool {
	if strings.HasPrefix(pattern, "*.") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(base, suffix)
	}

	if strings.HasSuffix(pattern, "/") {
		segment := strings.Trim(pattern, "/")
		return hasPathSegment(relPath, segment)
	}

	if strings.Contains(pattern, "/") {
		return strings.Contains(relPath, strings.Trim(pattern, "/"))
	}

	if pattern == base {
		return true
	}

	return hasPathSegment(relPath, pattern)
}

func hasPathSegment(relPath, segment string) bool {
	for _, part := range strings.Split(relPath, "/") {
		if part == segment {
			return true
		}
	}
	return false
}
