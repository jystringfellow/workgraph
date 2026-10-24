package workgraph

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"
)

const (
	databaseMigrationLockSuffix     = "-migration.lock"
	databaseUpgradeInProgressNotice = "Database upgrade in progress; capture status refreshes when it finishes."
	captureStatusUpgrading          = "unknown (database upgrade in progress)"
)

var (
	databaseMigrationLockTimeout = 10 * time.Minute
	databaseMigrationLockPoll    = 50 * time.Millisecond
)

var databaseSidecarSuffixes = []string{"-wal", "-shm", "-journal", databaseMigrationLockSuffix}

var ErrDatabaseUpgradeInProgress = errors.New("database upgrade in progress; try again when it finishes")

func createSchema(db *sql.DB) error {
	return createSchemaContext(context.Background(), db)
}

func createSchemaContext(ctx context.Context, db *sql.DB) error {
	path, err := mainDatabaseFile(db)
	if err != nil {
		return err
	}
	release, err := lockDatabaseMigration(ctx, path)
	if err != nil {
		return err
	}
	defer release()
	return prepareSchemaLocked(db, path)
}

func createSchemaUnlessUpgrading(db *sql.DB) error {
	path, err := mainDatabaseFile(db)
	if err != nil {
		return err
	}
	release, acquired, err := tryLockDatabaseMigration(path)
	if err != nil {
		return err
	}
	if !acquired {
		return ErrDatabaseUpgradeInProgress
	}
	defer release()
	return prepareSchemaLocked(db, path)
}

func prepareSchemaLocked(db *sql.DB, path string) error {
	enableWriteAheadLog(db)
	return migrateSchema(db)
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
