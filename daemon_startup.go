package workgraph

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const ExitDaemonStillStarting = 75

var (
	daemonReadyGrace         = 2 * time.Second
	daemonStartupStaleAfter  = 15 * time.Second
	daemonStartupMaxWait     = 5 * time.Minute
	daemonStartupHeartbeat   = time.Second
	daemonUpgradeReportAfter = 500 * time.Millisecond
	daemonReadyPollInterval  = 20 * time.Millisecond
	daemonLaunchedExitGrace  = 300 * time.Millisecond
)

const daemonUpgradingProgress = "Upgrading database…"

type daemonStartup struct {
	PID          int       `json:"pid"`
	DatabasePath string    `json:"database_path"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type DaemonStillStartingError struct {
	PID               int
	UpgradingDatabase bool
}

func (err *DaemonStillStartingError) Error() string {
	if err.UpgradingDatabase {
		return "workgraph capture is still starting (upgrading database); check workgraph status"
	}
	return "workgraph capture is still starting; check workgraph status"
}

type daemonReadyWait struct {
	homeDir      string
	databasePath string
	exited       <-chan struct{}
	progress     io.Writer
}

func daemonStartupPath(homeDir string) string {
	return filepath.Join(homeDir, "daemon.starting.json")
}

func writeDaemonStartup(homeDir string, startup daemonStartup) error {
	contents, err := json.Marshal(startup)
	if err != nil {
		return fmt.Errorf("encode daemon startup: %w", err)
	}
	path := daemonStartupPath(homeDir)
	temporary := path + ".tmp"
	if err := os.WriteFile(temporary, append(contents, '\n'), 0o600); err != nil {
		return fmt.Errorf("write daemon startup: %w", err)
	}
	if err := os.Rename(temporary, path); err != nil {
		return fmt.Errorf("write daemon startup: %w", err)
	}
	return nil
}

func readDaemonStartup(homeDir string) (daemonStartup, bool) {
	contents, err := os.ReadFile(daemonStartupPath(homeDir))
	if err != nil {
		return daemonStartup{}, false
	}
	var startup daemonStartup
	if err := json.Unmarshal(contents, &startup); err != nil {
		return daemonStartup{}, false
	}
	return startup, true
}

func liveDaemonStartup(homeDir string) (daemonStartup, bool) {
	startup, ok := readDaemonStartup(homeDir)
	if !ok || startup.PID <= 0 || !processRunning(startup.PID) {
		return daemonStartup{}, false
	}
	if time.Since(startup.UpdatedAt) > daemonStartupStaleAfter {
		return daemonStartup{}, false
	}
	return startup, true
}

func verifiedDaemonStartup(homeDir string) (daemonStartup, bool) {
	startup, ok := liveDaemonStartup(homeDir)
	if !ok || !processMatchesCaptureWorker(startup.PID, homeDir, startup.DatabasePath) {
		return daemonStartup{}, false
	}
	return startup, true
}

func removeDaemonStartup(homeDir string, pid int) {
	if startup, ok := readDaemonStartup(homeDir); ok && startup.PID != pid {
		return
	}
	if err := os.Remove(daemonStartupPath(homeDir)); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "workgraph capture worker: remove daemon startup: %v\n", err)
	}
}

func beginDaemonStartup(homeDir string, databasePath string) func() {
	startup := daemonStartup{PID: os.Getpid(), DatabasePath: databasePath, UpdatedAt: time.Now().UTC()}
	if err := writeDaemonStartup(homeDir, startup); err != nil {
		fmt.Fprintf(os.Stderr, "workgraph capture worker: %v\n", err)
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(daemonStartupHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				startup.UpdatedAt = time.Now().UTC()
				if err := writeDaemonStartup(homeDir, startup); err != nil {
					fmt.Fprintf(os.Stderr, "workgraph capture worker: %v\n", err)
				}
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			close(done)
			<-stopped
			removeDaemonStartup(homeDir, startup.PID)
		})
	}
}

func startingDaemonStatus(homeDir string, startup daemonStartup) DaemonStatus {
	status := DaemonStatus{
		Starting:          true,
		PID:               startup.PID,
		HomeDir:           homeDir,
		DatabasePath:      startup.DatabasePath,
		UpgradingDatabase: databaseMigrationInProgress(startup.DatabasePath),
	}
	status.Message = daemonStartingMessage(status)
	return status
}

func daemonStartingMessage(status DaemonStatus) string {
	headline := "workgraph capture is starting"
	if status.UpgradingDatabase {
		headline += " (upgrading database)"
	}
	return fmt.Sprintf("%s\nPID: %d\nHome: %s", headline, status.PID, status.HomeDir)
}

func readyDaemonStatus(homeDir string) (DaemonStatus, bool) {
	status, err := readDaemonState(homeDir)
	if err != nil || status.PID <= 0 || !processRunning(status.PID) || !daemonPIDMatches(homeDir, status.PID) {
		return status, false
	}
	status.Running = true
	return status, true
}

func waitForDaemonReady(wait daemonReadyWait) (DaemonStatus, error) {
	started := time.Now()
	exited := wait.exited
	var launchedExitedAt time.Time
	var lastStatus DaemonStatus
	var upgradingSince time.Time
	verifiedPID := 0
	upgradeReported := false
	for {
		status, ready := readyDaemonStatus(wait.homeDir)
		if ready {
			return status, nil
		}
		if status.PID != 0 {
			lastStatus = status
		}
		if exited != nil {
			select {
			case <-exited:
				exited = nil
				launchedExitedAt = time.Now()
			default:
			}
		}

		startup, alive := liveDaemonStartup(wait.homeDir)
		if alive && startup.PID != verifiedPID {
			if processMatchesCaptureWorker(startup.PID, wait.homeDir, startup.DatabasePath) {
				verifiedPID = startup.PID
			} else {
				alive = false
			}
		}
		if !alive {
			if status, ready := readyDaemonStatus(wait.homeDir); ready {
				return status, nil
			}
			if !launchedExitedAt.IsZero() {
				if time.Since(launchedExitedAt) < daemonLaunchedExitGrace {
					time.Sleep(daemonReadyPollInterval)
					continue
				}
				return DaemonStatus{}, fmt.Errorf("daemon did not become ready: capture worker exited during startup; see %s", daemonLogPath(wait.homeDir))
			}
			if time.Since(started) < daemonReadyGrace {
				time.Sleep(daemonReadyPollInterval)
				continue
			}
			if _, found := readDaemonStartup(wait.homeDir); found {
				return DaemonStatus{}, fmt.Errorf("daemon did not become ready: capture worker stopped reporting startup progress; see %s", daemonLogPath(wait.homeDir))
			}
			if lastStatus.PID != 0 {
				return DaemonStatus{}, fmt.Errorf("daemon did not become ready with pid %d", lastStatus.PID)
			}
			return DaemonStatus{}, fmt.Errorf("daemon did not become ready")
		}

		databasePath := wait.databasePath
		if databasePath == "" {
			databasePath = startup.DatabasePath
		}
		upgrading := databaseMigrationInProgress(databasePath)
		if !upgrading {
			upgradingSince = time.Time{}
		} else if upgradingSince.IsZero() {
			upgradingSince = time.Now()
		}
		if upgrading && !upgradeReported && time.Since(upgradingSince) >= daemonUpgradeReportAfter {
			upgradeReported = true
			if wait.progress != nil {
				fmt.Fprintln(wait.progress, daemonUpgradingProgress)
			}
		}
		if time.Since(started) >= daemonStartupMaxWait {
			return DaemonStatus{}, &DaemonStillStartingError{PID: startup.PID, UpgradingDatabase: upgrading}
		}
		time.Sleep(daemonReadyPollInterval)
	}
}
