// Package editor manages the external-editor round trip: resolve which
// editor to launch, seed a temp markdown file, and detect whether the user
// actually changed anything.
package editor

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Resolve picks the editor command: $VISUAL, then $EDITOR, then a platform
// fallback (notepad on Windows, nano/vi elsewhere). Values are split on
// whitespace so settings like `code --wait` work.
func Resolve() ([]string, error) {
	for _, env := range []string{"VISUAL", "EDITOR"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return strings.Fields(v), nil
		}
	}
	if runtime.GOOS == "windows" {
		return []string{"notepad"}, nil
	}
	for _, candidate := range []string{"nano", "vi"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return []string{candidate}, nil
		}
	}
	return nil, fmt.Errorf("no editor found: set the EDITOR environment variable")
}

// Session is one edit round trip through a temp file.
type Session struct {
	Path         string
	originalHash [32]byte
}

// NewSession writes initial to a temp .md file and closes it (Windows
// requires the handle closed before another process opens the file).
func NewSession(initial string) (*Session, error) {
	f, err := os.CreateTemp("", "drupaltui-*.md")
	if err != nil {
		return nil, fmt.Errorf("creating temp file: %w", err)
	}
	path := f.Name()
	_, werr := f.WriteString(initial)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(path)
		if werr != nil {
			return nil, fmt.Errorf("writing temp file: %w", werr)
		}
		return nil, fmt.Errorf("closing temp file: %w", cerr)
	}
	return &Session{Path: path, originalHash: sha256.Sum256([]byte(initial))}, nil
}

// Command builds the editor invocation for this session's file.
func (s *Session) Command() (*exec.Cmd, error) {
	parts, err := Resolve()
	if err != nil {
		return nil, err
	}
	args := append(parts[1:], s.Path)
	return exec.Command(parts[0], args...), nil
}

// Result reads the edited file back and reports whether it changed.
func (s *Session) Result() (body string, changed bool, err error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		return "", false, fmt.Errorf("reading edited file: %w", err)
	}
	body = normalizeNewlines(string(data))
	changed = sha256.Sum256([]byte(body)) != s.originalHash
	return body, changed, nil
}

// Cleanup removes the temp file. Safe to call multiple times.
func (s *Session) Cleanup() {
	if s.Path != "" {
		_ = os.Remove(s.Path)
	}
}

// normalizeNewlines converts CRLF (notepad's default) to LF so Windows edits
// don't litter the stored markdown with carriage returns — and so an
// untouched file with unchanged content hashes equal to the original.
func normalizeNewlines(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}
