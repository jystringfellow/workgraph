//go:build darwin || linux

package workgraph

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSecureDatabaseFilesDoesNotFollowSidecarSymlinks(t *testing.T) {
	dir := t.TempDir()
	databasePath := filepath.Join(dir, "workgraph.db")
	if err := os.WriteFile(databasePath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(databasePath, 0o644); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(dir, "unrelated-tool")
	if err := os.WriteFile(unrelated, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(unrelated, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(unrelated, databasePath+"-shm"); err != nil {
		t.Fatal(err)
	}

	if err := secureDatabaseFiles(databasePath); err != nil {
		t.Fatalf("secure database files: %v", err)
	}
	if info, err := os.Stat(unrelated); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("expected the symlink target to keep 0755, got %v %v", info.Mode().Perm(), err)
	}
	if info, err := os.Stat(databasePath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("expected the database to become 0600, got %v %v", info.Mode().Perm(), err)
	}
}

func TestSecureDatabaseFilesCreatesNewDatabaseUserOnly(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "workgraph.db")
	if err := secureDatabaseFiles(databasePath); err != nil {
		t.Fatalf("secure database files: %v", err)
	}
	if info, err := os.Stat(databasePath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("expected a new database file with 0600, got %v %v", info, err)
	}
}
