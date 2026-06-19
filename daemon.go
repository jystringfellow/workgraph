package workgraph

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var listDaemonProcesses = listSystemDaemonProcesses

type daemonProcess struct {
	PID     int
	Command string
}

// DaemonConfig controls background event capture.
type DaemonConfig struct {
	HomeDir         string
	DatabasePath    string
	WatchDirs       []string
	SlackToken      string
	SlackChannels   []string
	SlackListIDs    []string
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
	MonitoredConnectors []string `json:"monitored_connectors,omitempty"`
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
	for _, listID := range config.SlackListIDs {
		args = append(args, "--slack-list", listID)
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
		SlackListIDs:    config.SlackListIDs,
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
	if !processMatchesCaptureWorker(status.PID, status.HomeDir, status.DatabasePath) {
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

	processes, err := matchingCaptureWorkerProcessesForStatus(status)
	if err != nil {
		return DaemonStatus{}, err
	}
	if len(processes) == 0 && status.PID > 0 {
		processes = []daemonProcess{{PID: status.PID}}
	}
	for _, process := range processes {
		if err := signalDaemonProcess(process.PID, syscall.SIGTERM); err != nil && processRunning(process.PID) {
			return DaemonStatus{}, fmt.Errorf("stop daemon process %d: %w", process.PID, err)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !anyDaemonProcessRunning(processes) {
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
		MonitoredConnectors: append([]string(nil), status.MonitoredConnectors...),
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
		daemonWatchSummaryLine(status),
	}
	if len(status.MonitoredConnectors) > 0 {
		lines = append(lines, "Monitoring: "+strings.Join(status.MonitoredConnectors, ", "))
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
		daemonWatchSummaryLine(status),
	}
	if len(status.MonitoredConnectors) > 0 {
		lines = append(lines, "Monitoring: "+strings.Join(status.MonitoredConnectors, ", "))
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
		if last := lastRegisteredWatchDirectory(status.RegisteredWatchDirs); last != "" {
			*lines = append(*lines, "Last watched directory: "+last)
		}
		if status.WatchLimitPath != "" {
			*lines = append(*lines, "Next unwatched directory: "+status.WatchLimitPath)
		}
		*lines = append(*lines, "Prioritize important directories with workgraph settings add-watch.")
	}
}

func daemonWatchSummaryLine(status DaemonStatus) string {
	count := len(status.WatchDirs)
	switch count {
	case 0:
		return "Watching: no configured directories"
	case 1:
		return "Watching: 1 configured directory"
	default:
		return fmt.Sprintf("Watching: %d configured directories", count)
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

func signalDaemonProcess(pid int, signal os.Signal) error {
	process, err := os.FindProcess(pid)
	if err != nil {
		return fmt.Errorf("find daemon process: %w", err)
	}
	return process.Signal(signal)
}

func anyDaemonProcessRunning(processes []daemonProcess) bool {
	for _, process := range processes {
		if processRunning(process.PID) {
			return true
		}
	}
	return false
}

func matchingCaptureWorkerProcessesForStatus(status DaemonStatus) ([]daemonProcess, error) {
	processes, err := listDaemonProcesses()
	if err != nil {
		return nil, fmt.Errorf("list daemon processes: %w", err)
	}
	return matchingCaptureWorkerProcesses(status.HomeDir, status.DatabasePath, processes), nil
}

func processMatchesCaptureWorker(pid int, homeDir string, databasePath string) bool {
	processes, err := listDaemonProcesses()
	if err != nil {
		return true
	}
	for _, process := range matchingCaptureWorkerProcesses(homeDir, databasePath, processes) {
		if process.PID == pid {
			return true
		}
	}
	return false
}

func matchingCaptureWorkerProcesses(homeDir string, databasePath string, processes []daemonProcess) []daemonProcess {
	homeDir = cleanProcessPath(homeDir)
	databasePath = cleanProcessPath(databasePath)
	matches := []daemonProcess{}
	for _, process := range processes {
		if !strings.Contains(process.Command, "__capture-worker") {
			continue
		}
		args := strings.Fields(process.Command)
		processHome := cleanProcessPath(flagValue(args, "--home"))
		processDB := cleanProcessPath(flagValue(args, "--database"))
		if processHome != "" && homeDir != "" && processHome == homeDir {
			matches = append(matches, process)
			continue
		}
		if processDB != "" && databasePath != "" && processDB == databasePath {
			matches = append(matches, process)
		}
	}
	return matches
}

func listSystemDaemonProcesses() ([]daemonProcess, error) {
	output, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(output), "\n")
	processes := make([]daemonProcess, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pidText, command, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(pidText))
		if err != nil {
			continue
		}
		command = strings.TrimSpace(command)
		if command == "" {
			continue
		}
		processes = append(processes, daemonProcess{PID: pid, Command: command})
	}
	return processes, nil
}

func flagValue(args []string, name string) string {
	for i, arg := range args {
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(arg, name+"=") {
			return strings.TrimPrefix(arg, name+"=")
		}
	}
	return ""
}

func cleanProcessPath(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
