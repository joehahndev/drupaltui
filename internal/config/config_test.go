package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope", "config.yml"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.BaseURL != "" || cfg.ClientID != "" || cfg.TextFormat != "" {
		t.Errorf("expected empty config, got %+v", cfg)
	}
	if cfg.PageLimit != DefaultPageLimit {
		t.Errorf("PageLimit = %d, want %d", cfg.PageLimit, DefaultPageLimit)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.yml")
	in := &Config{
		BaseURL:    "https://example.com/",
		ClientID:   " drupaltui-client ",
		TextFormat: "markdown_easy",
		PageLimit:  25,
	}
	if err := in.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.BaseURL != "https://example.com" {
		t.Errorf("BaseURL = %q, want trailing slash trimmed", out.BaseURL)
	}
	if out.ClientID != "drupaltui-client" {
		t.Errorf("ClientID = %q, want %q", out.ClientID, "drupaltui-client")
	}
	if out.TextFormat != "markdown_easy" || out.PageLimit != 25 {
		t.Errorf("round trip mismatch: %+v", out)
	}
}

func TestSaveNeverWritesSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	cfg := &Config{BaseURL: "https://example.com", ClientID: "drupaltui-client", TextFormat: "markdown"}
	if err := cfg.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	lower := strings.ToLower(string(data))
	for _, forbidden := range []string{"password", "secret", "token"} {
		if strings.Contains(lower, forbidden) {
			t.Errorf("config file must never contain %q:\n%s", forbidden, data)
		}
	}
}

func TestNormalizeDefaultsPageLimit(t *testing.T) {
	cfg := &Config{PageLimit: -3}
	cfg.Normalize()
	if cfg.PageLimit != DefaultPageLimit {
		t.Errorf("PageLimit = %d, want %d", cfg.PageLimit, DefaultPageLimit)
	}
}
