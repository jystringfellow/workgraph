package workgraph

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	_ "github.com/mattn/go-sqlite3"
)

// ErrNotInitialized is returned when capture starts before workgraph init.
var ErrNotInitialized = errors.New("WorkGraph is not initialized")

// RunConfig controls foreground event capture.
type RunConfig struct {
	HomeDir      string
	DatabasePath string
	WatchDirs    []string
	// PollInterval is kept for tests and future fallback capture modes.
	PollInterval time.Duration
}

// RunStatus describes an active capture process.
type RunStatus struct {
	HomeDir      string
	DatabasePath string
	WatchDirs    []string
	Message      string
}

// CapturedEvent describes an event written by the foreground capture process.
type CapturedEvent struct {
	Type      string
	Operation string
	Path      string
}

// RunCapture watches local files and stores events until stopped.
type RunCapture struct {
	Status       RunStatus
	Events       <-chan CapturedEvent
	db           *sql.DB
	watcher      *fsnotify.Watcher
	homeDir      string
	databasePath string
	watchDirs    []string
	events       chan CapturedEvent
}

type fileEventPayload struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Size      int64  `json:"size,omitempty"`
}

// StartRun prepares foreground capture and returns once the watcher is ready.
func StartRun(config RunConfig) (*RunCapture, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return nil, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return nil, fmt.Errorf("resolve WorkGraph home: %w", err)
	}

	dbPath := config.DatabasePath
	if dbPath == "" {
		dbPath = filepath.Join(homeDir, "workgraph.db")
	}
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}

	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return nil, fmt.Errorf("check database: %w", err)
	}

	watchDirs, err := resolveWatchDirs(config.WatchDirs)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("open database: %w", err)
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("create file watcher: %w", err)
	}

	for _, watchDir := range watchDirs {
		if err := addWatchTree(watcher, watchDir, homeDir, dbPath); err != nil {
			watcher.Close()
			db.Close()
			return nil, err
		}
	}

	status := RunStatus{
		HomeDir:      homeDir,
		DatabasePath: dbPath,
		WatchDirs:    append([]string(nil), watchDirs...),
	}
	status.Message = runMessage(status)
	events := make(chan CapturedEvent, 128)

	return &RunCapture{
		Status:       status,
		Events:       events,
		db:           db,
		watcher:      watcher,
		homeDir:      homeDir,
		databasePath: dbPath,
		watchDirs:    watchDirs,
		events:       events,
	}, nil
}

// Run captures file events until the context is canceled.
func (capture *RunCapture) Run(ctx context.Context) error {
	defer capture.Close()

	for {
		select {
		case <-ctx.Done():
			return nil
		case event, ok := <-capture.watcher.Events:
			if !ok {
				return nil
			}
			if err := capture.handleEvent(event); err != nil {
				return err
			}
		case err, ok := <-capture.watcher.Errors:
			if !ok {
				return nil
			}
			if err != nil {
				return err
			}
		}
	}
}

// Close releases resources held by the capture process.
func (capture *RunCapture) Close() error {
	var closeErr error
	if capture.watcher != nil {
		closeErr = capture.watcher.Close()
		capture.watcher = nil
	}

	if capture.db == nil {
		return closeErr
	}

	err := capture.db.Close()
	capture.db = nil
	if err != nil {
		return err
	}

	if capture.events != nil {
		close(capture.events)
		capture.events = nil
	}

	return closeErr
}

func (capture *RunCapture) handleEvent(event fsnotify.Event) error {
	if shouldIgnorePath(event.Name, capture.homeDir, capture.databasePath) {
		return nil
	}

	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			return addWatchTree(capture.watcher, event.Name, capture.homeDir, capture.databasePath)
		}
		if err := capture.recordFileEvent(time.Now().UTC(), "created", event.Name); err != nil {
			return err
		}
	}

	if event.Has(fsnotify.Write) {
		if err := capture.recordFileEvent(time.Now().UTC(), "modified", event.Name); err != nil {
			return err
		}
	}

	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		if err := capture.recordFileEvent(time.Now().UTC(), "deleted", event.Name); err != nil {
			return err
		}
	}

	return nil
}

func (capture *RunCapture) recordFileEvent(now time.Time, operation string, path string) error {
	payload, err := json.Marshal(fileEventPayload{
		Path:      path,
		Operation: operation,
		Size:      fileSize(path),
	})
	if err != nil {
		return fmt.Errorf("encode file event: %w", err)
	}

	eventID, err := newEventID()
	if err != nil {
		return fmt.Errorf("create event id: %w", err)
	}

	_, err = capture.db.Exec(`INSERT INTO events
		(id, source, type, timestamp, payload_json, project, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		eventID,
		"file",
		"file."+operation,
		now.Format(time.RFC3339Nano),
		string(payload),
		inferProject(path, capture.watchDirs),
		now.Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("record file event: %w", err)
	}

	capture.events <- CapturedEvent{
		Type:      "file." + operation,
		Operation: operation,
		Path:      path,
	}

	return nil
}

func resolveWatchDirs(watchDirs []string) ([]string, error) {
	if len(watchDirs) == 0 {
		watchDirs = []string{"."}
	}

	resolved := make([]string, 0, len(watchDirs))
	for _, watchDir := range watchDirs {
		absPath, err := filepath.Abs(watchDir)
		if err != nil {
			return nil, fmt.Errorf("resolve watch directory: %w", err)
		}

		info, err := os.Stat(absPath)
		if err != nil {
			return nil, fmt.Errorf("watch directory %q: %w", absPath, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("watch path %q is not a directory", absPath)
		}

		resolved = append(resolved, absPath)
	}

	return resolved, nil
}

func addWatchTree(watcher *fsnotify.Watcher, root, homeDir, dbPath string) error {
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if shouldIgnorePath(path, homeDir, dbPath) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !entry.IsDir() {
			return nil
		}
		if err := watcher.Add(path); err != nil {
			return fmt.Errorf("watch directory %q: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("add watch tree %q: %w", root, err)
	}

	return nil
}

func shouldIgnorePath(path, homeDir, dbPath string) bool {
	if sameOrChild(path, homeDir) {
		return true
	}
	return path == dbPath
}

func sameOrChild(path, parent string) bool {
	rel, err := filepath.Rel(parent, path)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." {
		return false
	}
	return !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func inferProject(path string, watchDirs []string) string {
	for _, watchDir := range watchDirs {
		if sameOrChild(path, watchDir) {
			return filepath.Base(watchDir)
		}
	}
	return filepath.Base(filepath.Dir(path))
}

func runMessage(status RunStatus) string {
	lines := []string{
		"WorkGraph capture is running",
		"Home: " + status.HomeDir,
		"Database: " + status.DatabasePath,
	}
	for _, watchDir := range status.WatchDirs {
		lines = append(lines, "Watching: "+watchDir)
	}

	return strings.Join(lines, "\n")
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return 0
	}

	return info.Size()
}

func newEventID() (string, error) {
	randomBytes := make([]byte, 16)
	if _, err := rand.Read(randomBytes); err != nil {
		return "", err
	}

	return hex.EncodeToString(randomBytes), nil
}
