package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrivateConfigAndSanitizedErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"backend_tokens":{"test":"do-not-echo-this-secret"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := load(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := load(path); err == nil {
		t.Fatal("public-readable secrets accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"secret-that-must-not-be-logged":1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := load(path); err == nil || err.Error() != "invalid config JSON" {
		t.Fatal("config error not sanitized")
	}
}
