//go:build darwin || linux

package workgraph

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func lockDatabaseMigration(databasePath string) (func(), error) {
	if databasePath == "" {
		return func() {}, nil
	}
	file, err := os.OpenFile(databaseMigrationLockPath(databasePath), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open database migration lock: %w", err)
	}
	deadline := time.Now().Add(databaseMigrationLockTimeout)
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { file.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			file.Close()
			return nil, fmt.Errorf("lock database migration: %w", err)
		}
		if time.Now().After(deadline) {
			file.Close()
			return nil, ErrDatabaseUpgradeInProgress
		}
		time.Sleep(databaseMigrationLockPoll)
	}
}

func tryLockDatabaseMigration(databasePath string) (func(), bool, error) {
	if databasePath == "" {
		return func() {}, true, nil
	}
	file, err := os.OpenFile(databaseMigrationLockPath(databasePath), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, fmt.Errorf("open database migration lock: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("lock database migration: %w", err)
	}
	return func() { file.Close() }, true, nil
}

func databaseMigrationInProgress(databasePath string) bool {
	if databasePath == "" {
		return false
	}
	file, err := os.Open(databaseMigrationLockPath(databasePath))
	if err != nil {
		return false
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		return errors.Is(err, syscall.EWOULDBLOCK)
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return false
}
