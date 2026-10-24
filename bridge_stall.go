package workgraph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	bridgeStallFloor          = 30 * time.Minute
	DefaultBridgeDrainTimeout = 20 * time.Minute
)

var bridgeClients = []string{"codex", "claude-code"}

type bridgeStall struct {
	Age    string
	Detail string
}

func bridgeStallThreshold(interval time.Duration) time.Duration {
	if 3*interval > bridgeStallFloor {
		return 3 * interval
	}
	return bridgeStallFloor
}

func detectBridgeStall(homeDir string, state connectorRuntimeFile, id string, request CaptureRequest, now time.Time) (bridgeStall, bool) {
	if request.ID == "" || request.Kind != captureRequestKindCapture || connectorCaptureMode(state, id) != "bridged" || !connectorEnabled(state, id) {
		return bridgeStall{}, false
	}
	threshold := bridgeStallThreshold(connectorInterval(state, id, defaultConnectorInterval(id)))
	var stall bridgeStall
	if blocked := bridgeDrainBlockedReasons(homeDir); len(blocked) > 0 {
		if created, err := time.Parse(time.RFC3339Nano, request.CreatedAt); err == nil {
			stall.Age = formatBridgeAge(now.Sub(created))
		}
		stall.Detail = strings.Join(blocked, "; ")
		return stall, true
	}
	if created, err := time.Parse(time.RFC3339Nano, request.CreatedAt); err == nil && now.Sub(created) > threshold {
		stall.Age = formatBridgeAge(now.Sub(created))
		since := "no completed capture yet"
		if lastSuccess := state.entry(id).LastSuccess; lastSuccess != "" {
			since = "no completed capture since " + lastSuccess
		}
		stall.Detail = fmt.Sprintf("bridge stalled %s: %s", stall.Age, since)
	} else if heartbeat, found := freshestBridgeHeartbeat(homeDir); found && now.Sub(heartbeat) > threshold {
		stall.Age = formatBridgeAge(now.Sub(heartbeat))
		stall.Detail = "bridge worker heartbeat stale " + stall.Age
	} else {
		return bridgeStall{}, false
	}
	for _, drainError := range bridgeDrainErrors(homeDir) {
		stall.Detail += "; " + drainError
	}
	return stall, true
}

func bridgeStallWarning(homeDir string, databasePath string, now time.Time) string {
	if databaseMigrationInProgress(databasePath) {
		return "\n\n" + databaseUpgradeInProgressNotice
	}
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ""
	}
	requests, err := ListCaptureRequests(CaptureRequestListConfig{HomeDir: homeDir, DatabasePath: databasePath, Now: now})
	if err != nil {
		return ""
	}
	stalled := []string{}
	for _, request := range requests {
		if request.Status != "pending" && request.Status != "claimed" {
			continue
		}
		if stall, found := detectBridgeStall(homeDir, state, request.ConnectorID, request, now); found {
			stalled = append(stalled, fmt.Sprintf("%s (%s)", request.ConnectorID, stall.Age))
		}
	}
	if len(stalled) == 0 {
		return ""
	}
	sort.Strings(stalled)
	return fmt.Sprintf("\n\nBridge stalled: %s; run workgraph plugin doctor --client %s", strings.Join(stalled, ", "), bridgeClientHint(homeDir))
}

func bridgeClientHint(homeDir string) string {
	present := []string{}
	for _, client := range bridgeClients {
		for _, suffix := range []string{".launch-agent", ".heartbeat", ".last-error", ".blocked"} {
			if _, err := os.Stat(filepath.Join(homeDir, "bridge", client+suffix)); err == nil {
				present = append(present, client)
				break
			}
		}
	}
	if len(present) == 1 {
		return present[0]
	}
	return "<codex|claude-code>"
}

func freshestBridgeHeartbeat(homeDir string) (time.Time, bool) {
	var freshest time.Time
	found := false
	for _, client := range bridgeClients {
		heartbeat, ok := readBridgeHeartbeat(homeDir, client)
		if ok && (!found || heartbeat.After(freshest)) {
			freshest = heartbeat
			found = true
		}
	}
	return freshest, found
}

func readBridgeHeartbeat(homeDir string, client string) (time.Time, bool) {
	contents, err := os.ReadFile(bridgeHeartbeatPath(homeDir, client))
	if err != nil {
		return time.Time{}, false
	}
	heartbeat, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(string(contents)))
	if err != nil {
		return time.Time{}, false
	}
	return heartbeat, true
}

func bridgeDrainErrors(homeDir string) []string {
	messages := []string{}
	for _, client := range bridgeClients {
		if line := readBridgeDrainError(homeDir, client); line != "" {
			messages = append(messages, fmt.Sprintf("last drain error (%s): %s", client, line))
		}
	}
	return messages
}

func readBridgeDrainError(homeDir string, client string) string {
	contents, err := os.ReadFile(bridgeDrainErrorPath(homeDir, client))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(contents))
}

func recordBridgeDrainError(homeDir string, client string, output string) {
	line := lastNonEmptyLine(output)
	if line == "" {
		line = "client exited without output"
	}
	path := bridgeDrainErrorPath(homeDir, client)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		_ = os.WriteFile(path, []byte(line+"\n"), 0o600)
	}
}

func clearBridgeDrainError(homeDir string, client string) {
	_ = os.Remove(bridgeDrainErrorPath(homeDir, client))
}

func bridgeHeartbeatPath(homeDir string, client string) string {
	return filepath.Join(homeDir, "bridge", client+".heartbeat")
}

func bridgeDrainErrorPath(homeDir string, client string) string {
	return filepath.Join(homeDir, "bridge", client+".last-error")
}

func formatBridgeAge(age time.Duration) string {
	switch {
	case age >= 24*time.Hour:
		return fmt.Sprintf("%dd", int(age/(24*time.Hour)))
	case age >= time.Hour:
		return fmt.Sprintf("%dh", int(age/time.Hour))
	default:
		return fmt.Sprintf("%dm", int(age/time.Minute))
	}
}

func bridgeDrainBlockedReasons(homeDir string) []string {
	reasons := []string{}
	for _, client := range bridgeClients {
		contents, err := os.ReadFile(bridgeDrainBlockedPath(homeDir, client))
		if line := strings.TrimSpace(string(contents)); err == nil && line != "" {
			reasons = append(reasons, fmt.Sprintf("bridge drain blocked (%s): %s", client, line))
		}
	}
	return reasons
}

func recordBridgeDrainBlocked(homeDir string, client string, reason string) {
	path := bridgeDrainBlockedPath(homeDir, client)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		_ = os.WriteFile(path, []byte(reason+"\n"), 0o600)
	}
}

func clearBridgeDrainBlocked(homeDir string, client string) {
	_ = os.Remove(bridgeDrainBlockedPath(homeDir, client))
}

func bridgeDrainBlockedPath(homeDir string, client string) string {
	return filepath.Join(homeDir, "bridge", client+".blocked")
}

func lastBridgeDrainKind(homeDir string, client string) string {
	contents, err := os.ReadFile(bridgeDrainKindPath(homeDir, client))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(contents))
}

func recordBridgeDrainKind(homeDir string, client string, kind string) error {
	path := bridgeDrainKindPath(homeDir, client)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(kind+"\n"), 0o600)
}

func bridgeDrainKindPath(homeDir string, client string) string {
	return filepath.Join(homeDir, "bridge", client+".drain-kind")
}
