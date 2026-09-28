package editor

import (
	"os"
	"strings"
	"testing"
)

func TestResolvePrecedence(t *testing.T) {
	t.Setenv("VISUAL", "myvisual --flag")
	t.Setenv("EDITOR", "myeditor")
	parts, err := Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(parts) != 2 || parts[0] != "myvisual" || parts[1] != "--flag" {
		t.Errorf("VISUAL should win and be split: %v", parts)
	}

	t.Setenv("VISUAL", "")
	parts, err = Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(parts) != 1 || parts[0] != "myeditor" {
		t.Errorf("EDITOR fallback: %v", parts)
	}
}

func TestSessionRoundTripUnchanged(t *testing.T) {
	s, err := NewSession("hello\nworld\n")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Cleanup()

	body, changed, err := s.Result()
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if changed {
		t.Error("untouched file must report changed=false")
	}
	if body != "hello\nworld\n" {
		t.Errorf("body = %q", body)
	}
}

func TestSessionDetectsChange(t *testing.T) {
	s, err := NewSession("original")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Cleanup()

	if err := os.WriteFile(s.Path, []byte("edited"), 0o600); err != nil {
		t.Fatalf("simulating edit: %v", err)
	}
	body, changed, err := s.Result()
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if !changed || body != "edited" {
		t.Errorf("changed=%v body=%q", changed, body)
	}
}

func TestSessionNormalizesCRLF(t *testing.T) {
	s, err := NewSession("line one\nline two\n")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer s.Cleanup()

	// Notepad-style save: same content but CRLF line endings.
	if err := os.WriteFile(s.Path, []byte("line one\r\nline two\r\n"), 0o600); err != nil {
		t.Fatalf("simulating edit: %v", err)
	}
	body, changed, err := s.Result()
	if err != nil {
		t.Fatalf("Result: %v", err)
	}
	if strings.Contains(body, "\r") {
		t.Error("carriage returns must be normalized away")
	}
	if changed {
		t.Error("CRLF-only difference must not count as a change")
	}
}

func TestCleanupRemovesFile(t *testing.T) {
	s, err := NewSession("x")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	s.Cleanup()
	if _, err := os.Stat(s.Path); !os.IsNotExist(err) {
		t.Errorf("temp file %s still exists after Cleanup", s.Path)
	}
	s.Cleanup() // second call must not panic
}
