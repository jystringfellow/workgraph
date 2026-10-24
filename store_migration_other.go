//go:build !darwin && !linux

package workgraph

import "context"

func lockDatabaseMigration(ctx context.Context, databasePath string) (func(), error) {
	return func() {}, nil
}

func tryLockDatabaseMigration(databasePath string) (func(), bool, error) {
	return func() {}, true, nil
}

func databaseMigrationInProgress(databasePath string) bool {
	return false
}

func secureDatabaseFiles(databasePath string) error {
	return nil
}
