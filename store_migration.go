package workgraph

import (
	"database/sql"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"
)

const (
	databaseMigrationLockSuffix     = "-migration.lock"
	databaseUpgradeInProgressNotice = "Database upgrade in progress; capture status refreshes when it finishes."
)

var (
	databaseMigrationLockTimeout = 10 * time.Minute
	databaseMigrationLockPoll    = 50 * time.Millisecond
)

var databaseSidecarSuffixes = []string{"-wal", "-shm", "-journal", databaseMigrationLockSuffix}

func createSchema(db *sql.DB) error {
	path, err := mainDatabaseFile(db)
	if err != nil {
		return err
	}
	release, err := lockDatabaseMigration(path)
	if err != nil {
		return err
	}
	defer release()
	enableWriteAheadLog(db)
	return migrateSchema(db)
}

func createSchemaUnlessUpgrading(db *sql.DB, databasePath string) error {
	if databaseMigrationInProgress(databasePath) {
		return nil
	}
	return createSchema(db)
}

func mainDatabaseFile(db *sql.DB) (string, error) {
	rows, err := db.Query(`PRAGMA database_list`)
	if err != nil {
		return "", fmt.Errorf("inspect database file: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return "", fmt.Errorf("inspect database file: %w", err)
		}
		if name == "main" {
			return file, nil
		}
	}
	return "", rows.Err()
}

func enableWriteAheadLog(db *sql.DB) {
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode = WAL`).Scan(&mode); err != nil {
		log.Printf("workgraph store could not enable WAL journal mode: %v", err)
		return
	}
	if mode != "wal" && mode != "memory" {
		log.Printf("workgraph store journal mode remains %s", mode)
	}
}

func databaseMigrationLockPath(databasePath string) string {
	if resolved, err := filepath.EvalSymlinks(databasePath); err == nil {
		databasePath = resolved
	}
	return databasePath + databaseMigrationLockSuffix
}

func isDatabaseFile(path string, databasePath string) bool {
	if databasePath == "" {
		return false
	}
	if path == databasePath {
		return true
	}
	suffix, ok := strings.CutPrefix(path, databasePath)
	if !ok {
		return false
	}
	for _, sidecar := range databaseSidecarSuffixes {
		if suffix == sidecar {
			return true
		}
	}
	return false
}

func errDatabaseUpgradeInProgress() error {
	return fmt.Errorf("database upgrade in progress; try again when it finishes")
}
