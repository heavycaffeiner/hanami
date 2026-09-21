package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

type testConfig struct {
	HTTP struct {
		Address string `mapstructure:"address"`
	} `mapstructure:"http"`
}

func TestLoadAppliesDocumentedPrecedence(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "config.yaml")
	if err := os.WriteFile(path, []byte("http:\n  address: file:8080\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TEST_HTTP_ADDRESS", "env:8080")

	loaded, err := Load[testConfig](context.Background(), Source{
		File:      path,
		EnvPrefix: "TEST",
		Defaults:  map[string]any{"http.address": "default:8080"},
		Overrides: map[string]any{"http.address": "override:8080"},
	})
	if err != nil {
		t.Fatalf("Load returned an error: %v", err)
	}
	if loaded.HTTP.Address != "override:8080" {
		t.Fatalf("unexpected address: %s", loaded.HTTP.Address)
	}
}

func TestLoadRefusesUnreadableExplicitFile(t *testing.T) {
	_, err := Load[testConfig](context.Background(), Source{File: filepath.Join(t.TempDir(), "missing.yaml")})
	if err == nil {
		t.Fatal("Load accepted a missing explicit file")
	}
}
