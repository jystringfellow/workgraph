package workgraph

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	_ "github.com/mattn/go-sqlite3"
)

// ErrNotInitialized is returned when capture starts before workgraph init.
var ErrNotInitialized = errors.New("workgraph is not initialized")

var errWatchLimitReached = errors.New("watch limit reached")

// RunConfig controls foreground event capture.
type RunConfig struct {
	HomeDir               string
	DatabasePath          string
	WatchDirs             []string
	ConservativeWatchDirs []string
	// MaxWatchEntries bounds recursive watcher setup. Zero uses the default.
	MaxWatchEntries int
	// PollInterval is kept for tests and future fallback capture modes.
	PollInterval time.Duration
	// GitPollInterval controls local git commit capture while running.
	GitPollInterval time.Duration
	// GitHubPollInterval controls GitHub activity capture while running.
	GitHubPollInterval time.Duration
	// GitHubCommand is the gh-compatible executable used for GitHub polling.
	GitHubCommand string
	// SlackPollInterval controls Slack message capture while running.
	SlackPollInterval time.Duration
	// SlackToken is the Slack API bearer token used for read-only polling.
	SlackToken string
	// SlackChannels are explicit Slack channel ids to poll.
	SlackChannels []string
	// SlackIncludeDMs opts into Slack IM and MPIM discovery.
	SlackIncludeDMs bool
	// SlackAPIBaseURL overrides the Slack Web API base URL for tests.
	SlackAPIBaseURL string
	// SlackHTTPClient overrides the Slack API HTTP client for tests.
	SlackHTTPClient *http.Client
}

// RunStatus describes an active capture process.
type RunStatus struct {
	HomeDir               string
	DatabasePath          string
	WatchDirs             []string
	ConservativeWatchDirs []string
	IgnorePaths           []string
	IgnoreNames           []string
	WatchCount            int
	WatchLimit            int
	WatchLimitReached     bool
	WatchLimitPath        string
	RegisteredWatchDirs   []string
	Message               string
}

// CapturedEvent describes an event written by the foreground capture process.
type CapturedEvent struct {
	Type      string
	Operation string
	Path      string
	Project   string
	Summary   string
}

// RunCapture watches local files and stores events until stopped.
type RunCapture struct {
	Status              RunStatus
	Events              <-chan CapturedEvent
	db                  *sql.DB
	watcher             *fsnotify.Watcher
	homeDir             string
	databasePath        string
	watchDirs           []string
	ignorePaths         []string
	ignoreNames         []string
	watchBudget         *watchBudget
	gitPollInterval     time.Duration
	githubPollInterval  time.Duration
	githubCommand       string
	slackPollInterval   time.Duration
	slackToken          string
	slackChannels       []string
	slackIncludeDMs     bool
	slackAPIBaseURL     string
	slackHTTPClient     *http.Client
	slackCursors        map[string]string
	suppressedCreates   map[string]time.Time
	deleteCoalesceDelay time.Duration
	events              chan CapturedEvent
}

type fileEventPayload struct {
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Size      int64  `json:"size,omitempty"`
}

// StartRun prepares foreground capture and returns once the watcher is ready.
func StartRun(config RunConfig) (*RunCapture, error) {
	status, err := prepareRunStatus(config)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite3", status.DatabasePath)
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

	budget := newWatchBudget(config.MaxWatchEntries)
	registeredRoots := []string{}
	for _, watchDir := range status.WatchDirs {
		registered, err := addWatchRoot(watcher, watchDir, status.HomeDir, status.DatabasePath, status.IgnorePaths, status.IgnoreNames, budget)
		if err != nil {
			if errors.Is(err, errWatchLimitReached) {
				break
			}
			watcher.Close()
			db.Close()
			return nil, err
		}
		if registered {
			registeredRoots = append(registeredRoots, watchDir)
		}
	}
	for _, watchDir := range registeredRoots {
		conservative := containsPath(status.ConservativeWatchDirs, watchDir)
		if err := addWatchChildren(watcher, watchDir, watchDir, status.HomeDir, status.DatabasePath, status.IgnorePaths, status.IgnoreNames, budget, conservative); err != nil {
			watcher.Close()
			db.Close()
			return nil, err
		}
	}
	status.WatchCount = budget.count
	status.WatchLimit = budget.limit
	status.WatchLimitReached = budget.reached
	status.WatchLimitPath = budget.limitPath
	status.RegisteredWatchDirs = append([]string(nil), budget.registered...)

	slackToken := config.SlackToken
	slackChannels := append([]string(nil), config.SlackChannels...)
	slackIncludeDMs := config.SlackIncludeDMs
	slackAPIBaseURL := config.SlackAPIBaseURL
	if slackToken == "" && len(slackChannels) == 0 {
		if slackConfig, err := readSlackConnectorConfig(status.HomeDir); err == nil {
			slackToken = slackConfig.AccessToken
			slackChannels = append([]string(nil), slackConfig.Channels...)
			slackIncludeDMs = slackConfig.IncludeDMs
			if slackAPIBaseURL == "" {
				slackAPIBaseURL = slackConfig.APIBaseURL
			}
		}
	}

	status.Message = runMessage(status)
	events := make(chan CapturedEvent, 128)

	return &RunCapture{
		Status:              status,
		Events:              events,
		db:                  db,
		watcher:             watcher,
		homeDir:             status.HomeDir,
		databasePath:        status.DatabasePath,
		watchDirs:           status.WatchDirs,
		ignorePaths:         status.IgnorePaths,
		ignoreNames:         status.IgnoreNames,
		watchBudget:         budget,
		gitPollInterval:     gitPollInterval(config.GitPollInterval),
		githubPollInterval:  githubPollInterval(config.GitHubPollInterval),
		githubCommand:       config.GitHubCommand,
		slackPollInterval:   slackPollInterval(config.SlackPollInterval),
		slackToken:          slackToken,
		slackChannels:       slackChannels,
		slackIncludeDMs:     slackIncludeDMs,
		slackAPIBaseURL:     slackAPIBaseURL,
		slackHTTPClient:     config.SlackHTTPClient,
		slackCursors:        map[string]string{},
		suppressedCreates:   map[string]time.Time{},
		deleteCoalesceDelay: 75 * time.Millisecond,
		events:              events,
	}, nil
}

// Run captures file events until the context is canceled.
func (capture *RunCapture) Run(ctx context.Context) error {
	defer capture.Close()

	gitTicker := time.NewTicker(capture.gitPollInterval)
	defer gitTicker.Stop()
	githubTicker := time.NewTicker(capture.githubPollInterval)
	defer githubTicker.Stop()
	slackTicker := time.NewTicker(capture.slackPollInterval)
	defer slackTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-gitTicker.C:
			if err := capture.captureGitCommits(); err != nil {
				return err
			}
		case <-githubTicker.C:
			if err := capture.captureGitHubEvents(); err != nil {
				return err
			}
		case <-slackTicker.C:
			if err := capture.captureSlackEvents(); err != nil {
				return err
			}
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

func (capture *RunCapture) captureGitCommits() error {
	result, err := CaptureGitCommits(GitCaptureConfig{
		HomeDir:      capture.homeDir,
		DatabasePath: capture.databasePath,
		WatchDirs:    capture.watchDirs,
		MaxCommits:   20,
	})
	if err != nil {
		return err
	}
	for _, event := range result.Events {
		capture.events <- event
	}
	return err
}

func (capture *RunCapture) captureGitHubEvents() error {
	_, err := CaptureGitHubFromGH(GitHubCaptureConfig{
		HomeDir:       capture.homeDir,
		DatabasePath:  capture.databasePath,
		WatchDirs:     capture.watchDirs,
		GitHubCommand: capture.githubCommand,
	})
	return err
}

func (capture *RunCapture) captureSlackEvents() error {
	if capture.slackToken == "" {
		return nil
	}
	result, err := CaptureSlackFromAPI(SlackAPICaptureConfig{
		HomeDir:      capture.homeDir,
		DatabasePath: capture.databasePath,
		Token:        capture.slackToken,
		Channels:     capture.slackChannels,
		IncludeDMs:   capture.slackIncludeDMs,
		APIBaseURL:   capture.slackAPIBaseURL,
		HTTPClient:   capture.slackHTTPClient,
		Cursors:      capture.slackCursors,
	})
	if err != nil {
		return err
	}
	capture.slackCursors = result.Cursors
	return nil
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
	if shouldIgnorePath(event.Name, capture.homeDir, capture.databasePath, capture.ignorePaths, capture.ignoreNames) {
		return nil
	}
	if isTransientEditorPath(event.Name) {
		return nil
	}

	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
			err := addWatchTree(capture.watcher, event.Name, capture.homeDir, capture.databasePath, capture.ignorePaths, capture.ignoreNames, capture.watchBudget)
			capture.Status.WatchCount = capture.watchBudget.count
			capture.Status.WatchLimitReached = capture.watchBudget.reached
			capture.Status.WatchLimitPath = capture.watchBudget.limitPath
			capture.Status.RegisteredWatchDirs = append([]string(nil), capture.watchBudget.registered...)
			return err
		}
		if capture.shouldSuppressCreate(event.Name) {
			return nil
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
		if err := capture.recordDeleteOrReplace(event.Name); err != nil {
			return err
		}
	}

	return nil
}

func (capture *RunCapture) recordDeleteOrReplace(path string) error {
	time.Sleep(capture.deleteCoalesceDelay)
	if _, err := os.Stat(path); err == nil {
		capture.suppressedCreates[path] = time.Now().Add(500 * time.Millisecond)
		return capture.recordFileEvent(time.Now().UTC(), "modified", path)
	}
	return capture.recordFileEvent(time.Now().UTC(), "deleted", path)
}

func (capture *RunCapture) shouldSuppressCreate(path string) bool {
	deadline, ok := capture.suppressedCreates[path]
	if !ok {
		return false
	}
	if time.Now().After(deadline) {
		delete(capture.suppressedCreates, path)
		return false
	}
	return true
}

func isTransientEditorPath(path string) bool {
	name := filepath.Base(path)
	if name == ".DS_Store" {
		return true
	}
	return strings.Contains(name, ".sb-")
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

func readConfig(configPath string) (configFile, error) {
	contents, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return configFile{}, nil
		}
		return configFile{}, fmt.Errorf("read config: %w", err)
	}

	var config configFile
	if err := json.Unmarshal(contents, &config); err != nil {
		return configFile{}, fmt.Errorf("parse config: %w", err)
	}

	return config, nil
}

func prepareRunStatus(config RunConfig) (RunStatus, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return RunStatus{}, err
	}
	homeDir, err = filepath.Abs(homeDir)
	if err != nil {
		return RunStatus{}, fmt.Errorf("resolve workgraph home: %w", err)
	}

	dbPath := config.DatabasePath
	if dbPath == "" {
		dbPath = filepath.Join(homeDir, "workgraph.db")
	}
	dbPath, err = filepath.Abs(dbPath)
	if err != nil {
		return RunStatus{}, fmt.Errorf("resolve database path: %w", err)
	}

	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return RunStatus{}, fmt.Errorf("%w: run workgraph init", ErrNotInitialized)
		}
		return RunStatus{}, fmt.Errorf("check database: %w", err)
	}

	localConfig, err := readConfig(filepath.Join(homeDir, "config.json"))
	if err != nil {
		return RunStatus{}, err
	}

	watchDirsConfig := config.WatchDirs
	conservativeWatchDirsConfig := config.ConservativeWatchDirs
	if len(watchDirsConfig) == 0 && len(localConfig.WatchDirs) > 0 {
		watchDirsConfig = localConfig.WatchDirs
		conservativeWatchDirsConfig = localConfig.ConservativeWatchDirs
	}
	watchDirs, err := resolveWatchDirs(watchDirsConfig)
	if err != nil {
		return RunStatus{}, err
	}
	conservativeWatchDirs, err := resolveIgnorePaths(conservativeWatchDirsConfig)
	if err != nil {
		return RunStatus{}, err
	}
	ignorePaths, err := resolveIgnorePaths(localConfig.IgnorePaths)
	if err != nil {
		return RunStatus{}, err
	}

	return RunStatus{
		HomeDir:               homeDir,
		DatabasePath:          dbPath,
		WatchDirs:             watchDirs,
		ConservativeWatchDirs: conservativeWatchDirs,
		IgnorePaths:           ignorePaths,
		IgnoreNames:           append([]string(nil), localConfig.IgnoreNames...),
	}, nil
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

func resolveIgnorePaths(ignorePaths []string) ([]string, error) {
	resolved := make([]string, 0, len(ignorePaths))
	for _, ignorePath := range ignorePaths {
		absPath, err := filepath.Abs(ignorePath)
		if err != nil {
			return nil, fmt.Errorf("resolve ignored path: %w", err)
		}
		resolved = append(resolved, absPath)
	}
	return resolved, nil
}

func addWatchTree(watcher *fsnotify.Watcher, root, homeDir, dbPath string, ignorePaths []string, ignoreNames []string, budget *watchBudget) error {
	registered, err := addWatchRoot(watcher, root, homeDir, dbPath, ignorePaths, ignoreNames, budget)
	if err == nil && registered {
		err = addWatchChildren(watcher, root, root, homeDir, dbPath, ignorePaths, ignoreNames, budget, false)
	}
	if errors.Is(err, errWatchLimitReached) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("add watch tree %q: %w", root, err)
	}

	return nil
}

func addWatchRoot(watcher *fsnotify.Watcher, path, homeDir, dbPath string, ignorePaths []string, ignoreNames []string, budget *watchBudget) (bool, error) {
	if shouldIgnorePath(path, homeDir, dbPath, ignorePaths, ignoreNames) {
		return false, nil
	}
	if !canReadDirectory(path) {
		return false, nil
	}
	if !budget.canAdd(path) {
		return false, errWatchLimitReached
	}
	if err := watcher.Add(path); err != nil {
		if isPermissionError(err) || isUnsupportedSpecialFileError(err) {
			return false, nil
		}
		if isResourceLimitError(err) {
			budget.markReached(path)
			return false, errWatchLimitReached
		}
		return false, fmt.Errorf("watch directory %q: %w", path, err)
	}
	budget.noteAdded(path)
	return true, nil
}

func addWatchChildren(watcher *fsnotify.Watcher, root, path, homeDir, dbPath string, ignorePaths []string, ignoreNames []string, budget *watchBudget, conservativeRoot bool) error {
	entries, err := os.ReadDir(path)
	if err != nil {
		if isPermissionError(err) || isUnsupportedSpecialFileError(err) || isResourceLimitError(err) {
			return nil
		}
		return err
	}
	sortWatchEntries(entries)

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		child := filepath.Join(path, entry.Name())
		if shouldSkipImplicitTopLevelHiddenDir(root, path, child) {
			continue
		}
		recurse := true
		childConservative := conservativeRoot
		if conservativeRoot && samePath(path, root) {
			recurse = looksLikeWorkDirectory(child)
			childConservative = false
		}
		if err := addWatchTreeUnderRoot(watcher, root, child, homeDir, dbPath, ignorePaths, ignoreNames, budget, childConservative, recurse); err != nil {
			return err
		}
	}

	return nil
}

func addWatchTreeUnderRoot(watcher *fsnotify.Watcher, root, path, homeDir, dbPath string, ignorePaths []string, ignoreNames []string, budget *watchBudget, conservativeRoot bool, recurse bool) error {
	registered, err := addWatchRoot(watcher, path, homeDir, dbPath, ignorePaths, ignoreNames, budget)
	if err == nil && registered && recurse {
		err = addWatchChildren(watcher, root, path, homeDir, dbPath, ignorePaths, ignoreNames, budget, conservativeRoot)
	}
	if errors.Is(err, errWatchLimitReached) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("add watch tree %q: %w", path, err)
	}

	return nil
}

func shouldSkipImplicitTopLevelHiddenDir(root, parent, child string) bool {
	if !samePath(parent, root) {
		return false
	}
	if strings.HasPrefix(filepath.Base(root), ".") {
		return false
	}
	return strings.HasPrefix(filepath.Base(child), ".")
}

func looksLikeWorkDirectory(path string) bool {
	name := filepath.Base(path)
	switch name {
	case "Code", "Developer", "Projects", "Work", "repos", "source":
		return true
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if isProjectMarkerName(entry.Name()) {
			return true
		}
		if !entry.IsDir() && !isTransientEditorPath(entry.Name()) {
			return true
		}
	}
	return false
}

func isProjectMarkerName(name string) bool {
	switch name {
	case ".git", "go.mod", "package.json", "pyproject.toml", "Cargo.toml", "Gemfile", "composer.json", "pom.xml", "build.gradle", "mix.exs":
		return true
	default:
		return false
	}
}

func samePath(left, right string) bool {
	return filepath.Clean(left) == filepath.Clean(right)
}

func containsPath(paths []string, target string) bool {
	for _, path := range paths {
		if samePath(path, target) {
			return true
		}
	}
	return false
}

func sortWatchEntries(entries []fs.DirEntry) {
	sort.SliceStable(entries, func(i, j int) bool {
		left := entries[i].Name()
		right := entries[j].Name()
		leftPriority := watchEntryPriority(left)
		rightPriority := watchEntryPriority(right)
		if leftPriority != rightPriority {
			return leftPriority < rightPriority
		}
		return left < right
	})
}

func watchEntryPriority(name string) int {
	switch name {
	case "Desktop", "Documents", "Downloads", "Projects", "Code", "Work":
		return 0
	}
	if strings.HasPrefix(name, ".") {
		return 2
	}
	return 1
}

type watchBudget struct {
	limit      int
	count      int
	reached    bool
	limitPath  string
	registered []string
}

func newWatchBudget(limit int) *watchBudget {
	if limit <= 0 {
		limit = defaultMaxWatchEntries()
	}
	return &watchBudget{limit: limit}
}

func defaultMaxWatchEntries() int {
	if runtime.GOOS == "darwin" {
		return 128
	}
	return 4096
}

func gitPollInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 30 * time.Second
	}
	return interval
}

func githubPollInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 5 * time.Minute
	}
	return interval
}

func slackPollInterval(interval time.Duration) time.Duration {
	if interval <= 0 {
		return 2 * time.Minute
	}
	return interval
}

func (budget *watchBudget) canAdd(path string) bool {
	if budget.count >= budget.limit {
		budget.reached = true
		if budget.limitPath == "" {
			budget.limitPath = path
		}
		return false
	}
	return true
}

func (budget *watchBudget) noteAdded(path string) {
	budget.count++
	budget.registered = append(budget.registered, path)
}

func (budget *watchBudget) markReached(path string) {
	budget.reached = true
	if budget.limitPath == "" {
		budget.limitPath = path
	}
}

func canReadDirectory(path string) bool {
	dir, err := os.Open(path)
	if err != nil {
		return !isPermissionError(err)
	}
	defer dir.Close()

	_, err = dir.Readdirnames(1)
	return err == nil || errors.Is(err, io.EOF) || errors.Is(err, os.ErrNotExist) || errors.Is(err, fs.ErrNotExist)
}

func isPermissionError(err error) bool {
	if errors.Is(err, fs.ErrPermission) || errors.Is(err, os.ErrPermission) {
		return true
	}
	return runtime.GOOS == "darwin" && strings.Contains(strings.ToLower(err.Error()), "operation not permitted")
}

func isUnsupportedSpecialFileError(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "operation not supported on socket")
}

func isResourceLimitError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "too many open files")
}

func shouldIgnorePath(path, homeDir, dbPath string, ignorePaths []string, ignoreNames []string) bool {
	if sameOrChild(path, homeDir) {
		return true
	}
	if path == dbPath {
		return true
	}
	for _, ignorePath := range ignorePaths {
		if sameOrChild(path, ignorePath) {
			return true
		}
	}
	return hasIgnoredName(path, ignoreNames)
}

func hasIgnoredName(path string, ignoreNames []string) bool {
	for _, segment := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		for _, ignoreName := range ignoreNames {
			if segment == ignoreName {
				return true
			}
		}
	}
	return false
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
	if gitRoot := nearestGitRoot(path); gitRoot != "" {
		return filepath.Base(gitRoot)
	}
	for _, watchDir := range watchDirs {
		if sameOrChild(path, watchDir) {
			return filepath.Base(watchDir)
		}
	}
	return filepath.Base(filepath.Dir(path))
}

func nearestGitRoot(path string) string {
	current := path
	if info, err := os.Stat(current); err == nil && !info.IsDir() {
		current = filepath.Dir(current)
	}
	for {
		if info, err := os.Stat(filepath.Join(current, ".git")); err == nil && info.IsDir() {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}

func runMessage(status RunStatus) string {
	lines := []string{
		"workgraph capture is running",
		"Home: " + status.HomeDir,
		"Database: " + status.DatabasePath,
	}
	for _, watchDir := range status.WatchDirs {
		lines = append(lines, "Watching: "+watchDir)
	}
	if status.WatchLimitReached {
		lines = append(lines, fmt.Sprintf("Watch limit reached: %d/%d directories registered", status.WatchCount, status.WatchLimit))
		lines = append(lines, "Registered watch directories:")
		sample := watchDirectorySample(status.RegisteredWatchDirs)
		for _, watchDir := range sample {
			lines = append(lines, "Watching directory: "+watchDir)
		}
		if len(status.RegisteredWatchDirs) > len(sample) {
			lines = append(lines, fmt.Sprintf("... and %d more", len(status.RegisteredWatchDirs)-len(sample)))
		}
		if status.WatchLimitPath != "" {
			lines = append(lines, "First unwatched directory: "+status.WatchLimitPath)
		}
	}

	return strings.Join(lines, "\n")
}

func watchDirectorySample(watchDirs []string) []string {
	const maxSample = 10
	if len(watchDirs) <= maxSample {
		return watchDirs
	}
	return watchDirs[:maxSample]
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
