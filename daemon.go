package workgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// DaemonConfig controls background event capture.
type DaemonConfig struct {
	HomeDir         string
	DatabasePath    string
	WatchDirs       []string
	SlackToken      string
	SlackChannels   []string
	SlackIncludeDMs bool
	SlackAPIBaseURL string
}

// DaemonStatus describes the current background capture process.
type DaemonStatus struct {
	Running             bool     `json:"running"`
	PID                 int      `json:"pid"`
	HomeDir             string   `json:"home_dir"`
	DatabasePath        string   `json:"database_path"`
	WatchDirs           []string `json:"watch_dirs"`
	IgnorePaths         []string `json:"ignore_paths"`
	IgnoreNames         []string `json:"ignore_names"`
	WatchCount          int      `json:"watch_count"`
	WatchLimit          int      `json:"watch_limit"`
	WatchLimitReached   bool     `json:"watch_limit_reached"`
	WatchLimitPath      string   `json:"watch_limit_path"`
	RegisteredWatchDirs []string `json:"registered_watch_dirs"`
	Message             string   `json:"-"`
}

// StartDaemon starts background capture and writes daemon state under workgraph home.
func StartDaemon(config DaemonConfig) (DaemonStatus, error) {
	runStatus, err := prepareRunStatus(RunConfig{
		HomeDir:      config.HomeDir,
		DatabasePath: config.DatabasePath,
		WatchDirs:    config.WatchDirs,
	})
	if err != nil {
		return DaemonStatus{}, err
	}

	if existing, err := DaemonStatusForHome(runStatus.HomeDir); err == nil && existing.Running {
		existing.Message = daemonRunningMessage(existing)
		return existing, nil
	}

	if err := os.MkdirAll(runStatus.HomeDir, 0o755); err != nil {
		return DaemonStatus{}, fmt.Errorf("create daemon state directory: %w", err)
	}

	logFile, err := os.OpenFile(daemonLogPath(runStatus.HomeDir), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return DaemonStatus{}, fmt.Errorf("open daemon log: %w", err)
	}
	defer logFile.Close()

	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return DaemonStatus{}, fmt.Errorf("open daemon stdin: %w", err)
	}
	defer devNull.Close()

	executable, err := os.Executable()
	if err != nil {
		return DaemonStatus{}, fmt.Errorf("find executable: %w", err)
	}

	args := []string{executable, "__capture-worker", "--home", runStatus.HomeDir, "--database", runStatus.DatabasePath}
	for _, watchDir := range config.WatchDirs {
		args = append(args, "--watch", watchDir)
	}
	if config.SlackAPIBaseURL != "" {
		args = append(args, "--slack-api-base", config.SlackAPIBaseURL)
	}
	for _, channel := range config.SlackChannels {
		args = append(args, "--slack-channel", channel)
	}
	if config.SlackIncludeDMs {
		args = append(args, "--slack-include-dms")
	}
	env := os.Environ()
	if config.SlackToken != "" {
		env = append(env, "WORKGRAPH_SLACK_TOKEN="+config.SlackToken)
	}

	process, err := os.StartProcess(executable, args, &os.ProcAttr{
		Env:   env,
		Files: []*os.File{devNull, logFile, logFile},
	})
	if err != nil {
		return DaemonStatus{}, fmt.Errorf("start daemon process: %w", err)
	}
	if err := process.Release(); err != nil {
		return DaemonStatus{}, fmt.Errorf("release daemon process: %w", err)
	}

	status, err := waitForDaemonReady(runStatus.HomeDir)
	if err != nil {
		return DaemonStatus{}, err
	}
	status.Message = daemonStartedMessage(status)

	return status, nil
}

// RunDaemon runs capture in the current process until interrupted.
func RunDaemon(config DaemonConfig) error {
	ctx, stop := signalContext()
	defer stop()

	capture, err := StartRun(RunConfig{
		HomeDir:         config.HomeDir,
		DatabasePath:    config.DatabasePath,
		WatchDirs:       config.WatchDirs,
		SlackToken:      config.SlackToken,
		SlackChannels:   config.SlackChannels,
		SlackIncludeDMs: config.SlackIncludeDMs,
		SlackAPIBaseURL: config.SlackAPIBaseURL,
	})
	if err != nil {
		return err
	}
	defer removeDaemonState(capture.Status.HomeDir)

	status := daemonStatusFromRun(capture.Status, os.Getpid())
	if err := writeDaemonState(status); err != nil {
		capture.Close()
		return err
	}

	return capture.Run(ctx)
}

// DaemonStatusForConfig reports whether background capture is running.
func DaemonStatusForConfig(config DaemonConfig) (DaemonStatus, error) {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return DaemonStatus{}, err
	}
	return DaemonStatusForHome(homeDir)
}

// DaemonStatusForHome reports daemon state for a resolved or unresolved workgraph home path.
func DaemonStatusForHome(homeDir string) (DaemonStatus, error) {
	resolvedHome, err := filepath.Abs(homeDir)
	if err != nil {
		return DaemonStatus{}, fmt.Errorf("resolve workgraph home: %w", err)
	}

	status, err := readDaemonState(resolvedHome)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return daemonStoppedStatus(resolvedHome), nil
		}
		return DaemonStatus{}, err
	}
	if !processRunning(status.PID) {
		_ = removeDaemonState(resolvedHome)
		return daemonStoppedStatus(resolvedHome), nil
	}

	status.Running = true
	status.Message = daemonRunningMessage(status)
	return status, nil
}

// StopDaemon stops background capture and removes daemon state.
func StopDaemon(config DaemonConfig) (DaemonStatus, error) {
	status, err := DaemonStatusForConfig(config)
	if err != nil {
		return DaemonStatus{}, err
	}
	if !status.Running {
		status.Message = daemonStoppedMessage(status.HomeDir)
		return status, nil
	}

	process, err := os.FindProcess(status.PID)
	if err != nil {
		return DaemonStatus{}, fmt.Errorf("find daemon process: %w", err)
	}
	if err := process.Signal(syscall.SIGTERM); err != nil && processRunning(status.PID) {
		return DaemonStatus{}, fmt.Errorf("stop daemon process: %w", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !processRunning(status.PID) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := removeDaemonState(status.HomeDir); err != nil {
		return DaemonStatus{}, err
	}
	status.Running = false
	status.Message = daemonStopMessage(status.HomeDir)

	return status, nil
}

func daemonStatusFromRun(status RunStatus, pid int) DaemonStatus {
	return DaemonStatus{
		Running:             true,
		PID:                 pid,
		HomeDir:             status.HomeDir,
		DatabasePath:        status.DatabasePath,
		WatchDirs:           append([]string(nil), status.WatchDirs...),
		IgnorePaths:         append([]string(nil), status.IgnorePaths...),
		IgnoreNames:         append([]string(nil), status.IgnoreNames...),
		WatchCount:          status.WatchCount,
		WatchLimit:          status.WatchLimit,
		WatchLimitReached:   status.WatchLimitReached,
		WatchLimitPath:      status.WatchLimitPath,
		RegisteredWatchDirs: append([]string(nil), status.RegisteredWatchDirs...),
	}
}

func readDaemonState(homeDir string) (DaemonStatus, error) {
	contents, err := os.ReadFile(daemonStatePath(homeDir))
	if err != nil {
		return DaemonStatus{}, err
	}

	var status DaemonStatus
	if err := json.Unmarshal(contents, &status); err != nil {
		return DaemonStatus{}, fmt.Errorf("parse daemon state: %w", err)
	}

	return status, nil
}

func writeDaemonState(status DaemonStatus) error {
	contents, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("encode daemon state: %w", err)
	}
	contents = append(contents, '\n')

	if err := os.WriteFile(daemonStatePath(status.HomeDir), contents, 0o644); err != nil {
		return fmt.Errorf("write daemon state: %w", err)
	}
	if err := os.WriteFile(daemonPIDPath(status.HomeDir), []byte(strconv.Itoa(status.PID)+"\n"), 0o644); err != nil {
		return fmt.Errorf("write daemon pid: %w", err)
	}

	return nil
}

func waitForDaemonReady(homeDir string) (DaemonStatus, error) {
	deadline := time.Now().Add(2 * time.Second)
	var lastStatus DaemonStatus
	for time.Now().Before(deadline) {
		status, err := readDaemonState(homeDir)
		if err == nil {
			lastStatus = status
			if status.PID > 0 && processRunning(status.PID) {
				status.Running = true
				return status, nil
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if lastStatus.PID != 0 {
		return DaemonStatus{}, fmt.Errorf("daemon did not become ready with pid %d", lastStatus.PID)
	}
	return DaemonStatus{}, fmt.Errorf("daemon did not become ready")
}

func removeDaemonState(homeDir string) error {
	if err := os.Remove(daemonStatePath(homeDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove daemon state: %w", err)
	}
	if err := os.Remove(daemonPIDPath(homeDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove daemon pid: %w", err)
	}
	return nil
}

func daemonStatePath(homeDir string) string {
	return filepath.Join(homeDir, "daemon.json")
}

func daemonPIDPath(homeDir string) string {
	return filepath.Join(homeDir, "daemon.pid")
}

func daemonLogPath(homeDir string) string {
	return filepath.Join(homeDir, "daemon.log")
}

func daemonStoppedStatus(homeDir string) DaemonStatus {
	return DaemonStatus{
		Running: false,
		HomeDir: homeDir,
		Message: daemonStoppedMessage(homeDir),
	}
}

func daemonStartedMessage(status DaemonStatus) string {
	lines := []string{
		"workgraph capture started",
		"PID: " + strconv.Itoa(status.PID),
		"Home: " + status.HomeDir,
		"Database: " + status.DatabasePath,
	}
	for _, watchDir := range status.WatchDirs {
		lines = append(lines, "Watching: "+watchDir)
	}
	appendDaemonWatchLimitLine(&lines, status)
	return strings.Join(lines, "\n")
}

func daemonRunningMessage(status DaemonStatus) string {
	lines := []string{
		"workgraph capture is running",
		"PID: " + strconv.Itoa(status.PID),
		"Home: " + status.HomeDir,
		"Database: " + status.DatabasePath,
	}
	for _, watchDir := range status.WatchDirs {
		lines = append(lines, "Watching: "+watchDir)
	}
	for _, ignorePath := range status.IgnorePaths {
		lines = append(lines, "Ignoring path: "+ignorePath)
	}
	for _, ignoreName := range status.IgnoreNames {
		lines = append(lines, "Ignoring name: "+ignoreName)
	}
	appendDaemonWatchLimitLine(&lines, status)
	return strings.Join(lines, "\n")
}

func appendDaemonWatchLimitLine(lines *[]string, status DaemonStatus) {
	if status.WatchLimitReached {
		*lines = append(*lines, fmt.Sprintf("Watch limit reached: %d/%d directories registered", status.WatchCount, status.WatchLimit))
		*lines = append(*lines, "Registered watch directories:")
		sample := watchDirectorySample(status.RegisteredWatchDirs)
		for _, watchDir := range sample {
			*lines = append(*lines, "Watching directory: "+watchDir)
		}
		if len(status.RegisteredWatchDirs) > len(sample) {
			*lines = append(*lines, fmt.Sprintf("... and %d more", len(status.RegisteredWatchDirs)-len(sample)))
		}
		if status.WatchLimitPath != "" {
			*lines = append(*lines, "First unwatched directory: "+status.WatchLimitPath)
		}
	}
}

func daemonStoppedMessage(homeDir string) string {
	if homeDir == "" {
		return "workgraph capture is not running"
	}
	return "workgraph capture is not running\nHome: " + homeDir
}

func daemonStopMessage(homeDir string) string {
	return "workgraph capture stopped\nHome: " + homeDir
}

func processRunning(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
