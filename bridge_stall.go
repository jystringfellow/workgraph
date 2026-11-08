package workgraph

import (
	"encoding/json"
	"errors"
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

var bridgeClients = []string{"codex", "claude-code", "copilot", "agy"}

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
	if blocked := append(bridgeDrainBlockedReasons(homeDir), bridgeSignedOutReasons(homeDir)...); len(blocked) > 0 {
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
	state, err := readConnectorRuntimeFile(homeDir)
	if err != nil {
		return ""
	}
	requests, err := ListCaptureRequests(CaptureRequestListConfig{HomeDir: homeDir, DatabasePath: databasePath, Now: now})
	if errors.Is(err, ErrDatabaseUpgradeInProgress) {
		return "\n\n" + databaseUpgradeInProgressNotice
	}
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
	if signedOut := bridgeSignedOutClients(homeDir); len(signedOut) > 0 {
		remedies := make([]string, 0, len(signedOut))
		for _, client := range signedOut {
			remedies = append(remedies, bridgeSignInRemedy(client))
		}
		return fmt.Sprintf("\n\nBridge signed out: %s; %s", strings.Join(stalled, ", "), strings.Join(remedies, "; "))
	}
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
	return "<codex|claude-code|copilot|agy>"
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

var bridgeSignInFailurePhrases = []string{
	"failed to authenticate",
	"not logged in",
	"please run /login",
	"oauth session expired",
	"codex login",
	"copilot login",
}

func isBridgeSignInFailure(output string) bool {
	lowered := strings.ToLower(output)
	for _, phrase := range bridgeSignInFailurePhrases {
		if strings.Contains(lowered, phrase) {
			return true
		}
	}
	return false
}

type bridgeSignOut struct {
	Error     string `json:"error"`
	ConfigDir string `json:"config_dir,omitempty"`
}

func recordBridgeSignedOut(homeDir string, client string, output string, configDir string) {
	contents, err := json.Marshal(bridgeSignOut{Error: lastNonEmptyLine(output), ConfigDir: configDir})
	if err != nil {
		return
	}
	path := bridgeSignedOutPath(homeDir, client)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err == nil {
		_ = os.WriteFile(path, append(contents, '\n'), 0o600)
	}
}

func clearBridgeSignedOut(homeDir string, client string) {
	_ = os.Remove(bridgeSignedOutPath(homeDir, client))
}

func clearBridgeSignedOutFor(homeDir string, client string, configDir string) {
	if signOut, signedOut := readBridgeSignedOut(homeDir, client); signedOut && signOut.ConfigDir == configDir {
		clearBridgeSignedOut(homeDir, client)
	}
}

func readBridgeSignedOut(homeDir string, client string) (bridgeSignOut, bool) {
	contents, err := os.ReadFile(bridgeSignedOutPath(homeDir, client))
	if err != nil {
		return bridgeSignOut{}, false
	}
	var signOut bridgeSignOut
	if err := json.Unmarshal(contents, &signOut); err != nil {
		signOut.Error = strings.TrimSpace(string(contents))
	}
	return signOut, true
}

func bridgeSignedOutClients(homeDir string) []string {
	clients := []string{}
	for _, client := range bridgeClients {
		if _, signedOut := readBridgeSignedOut(homeDir, client); signedOut {
			clients = append(clients, client)
		}
	}
	return clients
}

func bridgeSignInRemedy(client string) string {
	if _, err := bridgeLoginArgs(client); err != nil {
		return "sign in with " + client + " directly"
	}
	return "run workgraph bridge login --client " + client
}

func bridgeSignedOutReasons(homeDir string) []string {
	reasons := []string{}
	for _, client := range bridgeSignedOutClients(homeDir) {
		signOut, _ := readBridgeSignedOut(homeDir, client)
		reasons = append(reasons, fmt.Sprintf("bridge signed out (%s, %s): %s; %s",
			client, signedInClientBindingLabel(signedInClientBinding{ConfigDir: signOut.ConfigDir}), signOut.Error, bridgeSignInRemedy(client)))
	}
	return reasons
}

func bridgeSignedOutPath(homeDir string, client string) string {
	return filepath.Join(homeDir, "bridge", client+".signed-out")
}
