//go:build darwin || linux

package workgraph

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func lockDatabaseMigration(ctx context.Context, databasePath string) (func(), error) {
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
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(databaseMigrationLockPoll):
		}
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

func secureDatabaseFiles(databasePath string) error {
	file, err := os.OpenFile(databasePath, os.O_CREATE|os.O_EXCL|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err == nil {
		return file.Close()
	}
	if !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("create database: %w", err)
	}
	for _, path := range []string{databasePath, databasePath + "-wal", databasePath + "-shm", databasePath + "-journal"} {
		if err := restrictOwnedRegularFile(path); err != nil {
			return err
		}
	}
	return nil
}

func restrictOwnedRegularFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if !info.Mode().IsRegular() || !ownedByCurrentUser(info) || info.Mode().Perm()&0o077 == 0 {
		return nil
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return fmt.Errorf("inspect %s: %w", path, err)
	}
	if !opened.Mode().IsRegular() || !ownedByCurrentUser(opened) || !os.SameFile(info, opened) {
		return nil
	}
	if err := file.Chmod(0o600); err != nil {
		return fmt.Errorf("restrict %s: %w", path, err)
	}
	return nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == os.Getuid()
}
