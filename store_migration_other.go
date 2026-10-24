//go:build !darwin && !linux

package workgraph

func lockDatabaseMigration(databasePath string) (func(), error) {
	return func() {}, nil
}

func databaseMigrationInProgress(databasePath string) bool {
	return false
}
