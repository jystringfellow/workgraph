//go:build !darwin && !linux

package workgraph

func lockDatabaseMigration(databasePath string) (func(), error) {
	return func() {}, nil
}

func tryLockDatabaseMigration(databasePath string) (func(), bool, error) {
	return func() {}, true, nil
}

func databaseMigrationInProgress(databasePath string) bool {
	return false
}
