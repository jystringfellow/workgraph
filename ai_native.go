package workgraph

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// AINativeSessionConfig controls one tool-native identity binding callback.
type AINativeSessionConfig struct {
	HomeDir      string
	DatabasePath string
	SessionID    string
	Tool         string
	Input        io.Reader
}

// AINativeSessionResult identifies the immutable native binding event.
type AINativeSessionResult struct {
	SessionID       string
	NativeSessionID string
	EventID         string
}

// AIResumeConfig controls one explicit native continuation launch.
type AIResumeConfig struct {
	HomeDir          string
	DatabasePath     string
	SessionID        string
	ProcessInspector AIProcessInspector
	Stdin            io.Reader
	Stdout           io.Writer
	Stderr           io.Writer
}

// AIResumeResult reports the newly wrapped continuation lifetime.
type AIResumeResult struct {
	PredecessorSessionID string
	SessionID            string
	ExitCode             int
}

type aiNativeLifecycleCallback struct {
	SessionID     string `json:"session_id"`
	CWD           string `json:"cwd"`
	HookEventName string `json:"hook_event_name"`
	Source        string `json:"source"`
	Reason        string `json:"reason"`
}

type aiNativeAdapter struct {
	Tool            string
	ResumePrefix    []string
	ResumeWithEqual bool
	BindingStrategy string
}

var aiNativeAdapters = map[string]aiNativeAdapter{
	"codex":    {Tool: "codex", ResumePrefix: []string{"resume"}, BindingStrategy: "lifecycle-hook"},
	"claude":   {Tool: "claude", ResumePrefix: []string{"--resume"}, BindingStrategy: "lifecycle-hook"},
	"copilot":  {Tool: "copilot", ResumePrefix: []string{"--resume"}, ResumeWithEqual: true, BindingStrategy: "assigned-id"},
	"opencode": {Tool: "opencode", ResumePrefix: []string{"--session"}, BindingStrategy: "opencode-plugin"},
}

// BindAINativeSession stores only adapter-allowlisted identity metadata.
func BindAINativeSession(config AINativeSessionConfig) (AINativeSessionResult, error) {
	tool := strings.TrimSpace(config.Tool)
	adapter, supported := aiNativeAdapters[tool]
	if !supported || (adapter.BindingStrategy != "lifecycle-hook" && adapter.BindingStrategy != "opencode-plugin") {
		return AINativeSessionResult{}, fmt.Errorf("AI tool %q has no verified native adapter", tool)
	}
	callback, err := parseAINativeLifecycleCallback(tool, config.Input)
	if err != nil {
		return AINativeSessionResult{}, err
	}
	sessionID := strings.TrimSpace(config.SessionID)
	environmentSessionID := strings.TrimSpace(os.Getenv("WORKGRAPH_AI_SESSION_ID"))
	if sessionID == "" {
		sessionID = environmentSessionID
	} else if environmentSessionID != "" && sessionID != environmentSessionID {
		return AINativeSessionResult{}, errors.New("explicit session id disagrees with WORKGRAPH_AI_SESSION_ID")
	}
	if sessionID == "" {
		return AINativeSessionResult{}, errors.New("workgraph AI session id is required")
	}

	status, err := prepareRunStatus(RunConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return AINativeSessionResult{}, err
	}
	db, err := sql.Open("sqlite3", status.DatabasePath)
	if err != nil {
		return AINativeSessionResult{}, fmt.Errorf("open AI event database: %w", err)
	}
	defer db.Close()

	var startedJSON, project string
	readStarted := func() error {
		return db.QueryRow(`SELECT payload_json, COALESCE(project, '') FROM events
			WHERE id = ? AND source = 'ai' AND type = 'ai.session_started'`, "ai.session_started:"+sessionID).Scan(&startedJSON, &project)
	}
	err = readStarted()
	if errors.Is(err, sql.ErrNoRows) && environmentSessionID == sessionID {
		deadline := time.Now().Add(2 * time.Second)
		for errors.Is(err, sql.ErrNoRows) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			err = readStarted()
		}
	}
	if errors.Is(err, sql.ErrNoRows) {
		return AINativeSessionResult{}, fmt.Errorf("AI session %q was not found", sessionID)
	}
	if err != nil {
		return AINativeSessionResult{}, fmt.Errorf("read AI session start: %w", err)
	}
	var started aiStartedPayload
	if err := json.Unmarshal([]byte(startedJSON), &started); err != nil || started.SchemaVersion != aiSchemaVersion {
		return AINativeSessionResult{}, errors.New("AI session start payload is unsupported or invalid")
	}
	if started.Tool != tool {
		return AINativeSessionResult{}, fmt.Errorf("native adapter %q does not match started tool %q", tool, started.Tool)
	}

	digest := sha256.Sum256([]byte(tool + "\x00" + callback.SessionID))
	eventID := "ai.session_native_bound:" + sessionID + ":" + hex.EncodeToString(digest[:])
	var existing string
	if err := db.QueryRow(`SELECT payload_json FROM events WHERE id = ?`, eventID).Scan(&existing); err == nil {
		var stored aiNativeBoundPayload
		if json.Unmarshal([]byte(existing), &stored) == nil && stored.SessionID == sessionID && stored.Tool == tool && stored.NativeSessionID == callback.SessionID {
			return AINativeSessionResult{SessionID: sessionID, NativeSessionID: callback.SessionID, EventID: eventID}, nil
		}
		return AINativeSessionResult{}, fmt.Errorf("native binding event %q contains different data", eventID)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return AINativeSessionResult{}, fmt.Errorf("read native binding: %w", err)
	}

	predecessor, err := findLatestAISessionByNativeID(db, tool, callback.SessionID, sessionID)
	if err != nil {
		return AINativeSessionResult{}, fmt.Errorf("find native predecessor: %w", err)
	}
	payload := aiNativeBoundPayload{
		SchemaVersion: aiSchemaVersion, SessionID: sessionID, Tool: tool,
		NativeSessionID: callback.SessionID, Source: callback.Source,
		PredecessorSessionID: predecessor,
	}
	if err := insertAIEvent(db, eventID, "ai.session_native_bound", time.Now().UTC(), project, "AI session native identity bound ("+tool+")", payload); err != nil {
		return AINativeSessionResult{}, fmt.Errorf("persist AI native binding: %w", err)
	}
	return AINativeSessionResult{SessionID: sessionID, NativeSessionID: callback.SessionID, EventID: eventID}, nil
}

// ResumeAISession launches the verified native continuation as a new lifetime.
func ResumeAISession(config AIResumeConfig) (AIResumeResult, error) {
	projections, _, err := loadAISessionProjections(config.HomeDir, config.DatabasePath, config.ProcessInspector)
	if err != nil {
		return AIResumeResult{}, err
	}
	var selected *aiSessionProjection
	for index := range projections {
		if projections[index].Summary.SessionID == config.SessionID {
			selected = &projections[index]
			break
		}
	}
	if selected == nil {
		return AIResumeResult{}, fmt.Errorf("AI session %q was not found", config.SessionID)
	}
	if selected.Summary.Status != "ended" && selected.Summary.Status != "interrupted" {
		return AIResumeResult{}, fmt.Errorf("AI session status %q cannot be resumed safely", selected.Summary.Status)
	}
	if selected.Summary.NativeSessionID == "" {
		return AIResumeResult{}, errors.New("native resume is unavailable: no native session id was recorded")
	}
	adapter, supported := aiNativeAdapters[selected.Summary.Tool]
	if !supported {
		return AIResumeResult{}, fmt.Errorf("native resume is unavailable: AI tool %q has no verified native adapter", selected.Summary.Tool)
	}
	if selected.Started == nil || selected.Started.ToolPath == "" {
		return AIResumeResult{}, errors.New("native resume is unavailable: no resolved tool path was recorded")
	}
	toolPath, err := canonicalAIPath(selected.Started.ToolPath)
	if err != nil {
		return AIResumeResult{}, fmt.Errorf("resolve stored AI tool path: %w", err)
	}
	if filepath.Base(toolPath) != selected.Summary.Tool {
		return AIResumeResult{}, errors.New("stored AI tool path does not match the verified adapter")
	}
	if selected.LatestObserved == nil || selected.LatestObserved.CWD == "" {
		return AIResumeResult{}, errors.New("native resume is unavailable: no working directory was recorded")
	}
	resumeArgs := aiNativeResumeArguments(adapter, selected.Summary.NativeSessionID)
	result, err := RunAISession(AIRunConfig{
		HomeDir: config.HomeDir, DatabasePath: config.DatabasePath,
		WorkingDir:           selected.LatestObserved.CWD,
		Command:              append([]string{toolPath}, resumeArgs...),
		PredecessorSessionID: selected.Summary.SessionID,
		Stdin:                config.Stdin, Stdout: config.Stdout, Stderr: config.Stderr,
	})
	if err != nil {
		return AIResumeResult{}, err
	}
	return AIResumeResult{PredecessorSessionID: selected.Summary.SessionID, SessionID: result.SessionID, ExitCode: result.ExitCode}, nil
}

func parseAINativeLifecycleCallback(tool string, input io.Reader) (aiNativeLifecycleCallback, error) {
	label := aiNativeAdapterLabel(tool)
	if input == nil {
		return aiNativeLifecycleCallback{}, fmt.Errorf("%s lifecycle callback stdin is required", label)
	}
	contents, err := io.ReadAll(io.LimitReader(input, 65537))
	if err != nil {
		return aiNativeLifecycleCallback{}, fmt.Errorf("read %s lifecycle callback", label)
	}
	if len(contents) > 65536 {
		return aiNativeLifecycleCallback{}, fmt.Errorf("%s lifecycle callback exceeds 65,536 bytes", label)
	}
	if !utf8.Valid(contents) {
		return aiNativeLifecycleCallback{}, fmt.Errorf("%s lifecycle callback must be valid UTF-8", label)
	}
	decoder := json.NewDecoder(bytes.NewReader(contents))
	var callback aiNativeLifecycleCallback
	if err := decoder.Decode(&callback); err != nil {
		return aiNativeLifecycleCallback{}, fmt.Errorf("%s lifecycle callback must be one JSON object", label)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return aiNativeLifecycleCallback{}, fmt.Errorf("%s lifecycle callback contains trailing JSON data", label)
	}
	callback.SessionID = strings.TrimSpace(callback.SessionID)
	if !validAINativeSessionID(callback.SessionID) {
		return aiNativeLifecycleCallback{}, fmt.Errorf("%s lifecycle callback has an invalid session id", label)
	}
	switch callback.HookEventName {
	case "SessionStart":
		if !validAINativeLifecycleSource(tool, callback.Source) {
			return aiNativeLifecycleCallback{}, fmt.Errorf("%s SessionStart callback has an invalid source", label)
		}
	case "SessionEnd":
		if tool == "opencode" {
			return aiNativeLifecycleCallback{}, errors.New("OpenCode native binding requires a session start callback")
		}
		callback.Source = "end"
	default:
		return aiNativeLifecycleCallback{}, fmt.Errorf("%s native binding requires a session lifecycle callback", label)
	}
	return callback, nil
}

func aiNativeAdapterLabel(tool string) string {
	switch tool {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	case "copilot":
		return "Copilot"
	case "opencode":
		return "OpenCode"
	default:
		return "AI tool"
	}
}

func validAINativeLifecycleSource(tool, source string) bool {
	switch tool {
	case "codex":
		return source == "startup" || source == "resume" || source == "clear" || source == "compact"
	case "claude":
		return source == "startup" || source == "resume" || source == "clear" || source == "compact" || source == "fork"
	case "opencode":
		return source == "startup" || source == "resume"
	default:
		return false
	}
}

func aiNativeResumeArguments(adapter aiNativeAdapter, nativeSessionID string) []string {
	if adapter.ResumeWithEqual {
		return []string{adapter.ResumePrefix[0] + "=" + nativeSessionID}
	}
	result := append([]string{}, adapter.ResumePrefix...)
	return append(result, nativeSessionID)
}

func validAINativeSessionID(value string) bool {
	if value == "" || len([]byte(value)) > 512 {
		return false
	}
	for _, character := range value {
		if character <= 0x1f || (character >= 0x7f && character <= 0x9f) {
			return false
		}
	}
	return true
}

func aiRecognizeNativeResume(tool string, args []string) (string, bool) {
	switch tool {
	case "codex":
		for index, argument := range args {
			if argument != "resume" {
				continue
			}
			for _, candidate := range args[index+1:] {
				if candidate == "--last" {
					return "", true
				}
				if strings.HasPrefix(candidate, "-") {
					continue
				}
				return validAINativeSelection(candidate, false), true
			}
			return "", true
		}
	case "claude":
		for index, argument := range args {
			switch {
			case argument == "--continue" || argument == "-c":
				return "", true
			case argument == "--resume" || argument == "-r":
				if index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
					return validAINativeSelection(args[index+1], false), true
				}
				return "", true
			case strings.HasPrefix(argument, "--resume="):
				return validAINativeSelection(strings.TrimPrefix(argument, "--resume="), false), true
			case strings.HasPrefix(argument, "-r="):
				return validAINativeSelection(strings.TrimPrefix(argument, "-r="), false), true
			}
		}
	case "copilot":
		for index, argument := range args {
			switch {
			case argument == "--continue" || argument == "-c":
				return "", true
			case argument == "--session-id" || argument == "--resume" || argument == "-r":
				if index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
					return validAINativeSelection(args[index+1], true), true
				}
				return "", true
			case strings.HasPrefix(argument, "--session-id="):
				return validAINativeSelection(strings.TrimPrefix(argument, "--session-id="), true), true
			case strings.HasPrefix(argument, "--resume="):
				return validAINativeSelection(strings.TrimPrefix(argument, "--resume="), true), true
			case strings.HasPrefix(argument, "-r="):
				return validAINativeSelection(strings.TrimPrefix(argument, "-r="), true), true
			}
		}
	case "opencode":
		for index, argument := range args {
			switch {
			case argument == "--continue" || argument == "-c":
				return "", true
			case argument == "--session" || argument == "-s":
				if index+1 < len(args) && !strings.HasPrefix(args[index+1], "-") {
					return validAINativeSelection(args[index+1], false), true
				}
				return "", true
			case strings.HasPrefix(argument, "--session="):
				return validAINativeSelection(strings.TrimPrefix(argument, "--session="), false), true
			case strings.HasPrefix(argument, "-s="):
				return validAINativeSelection(strings.TrimPrefix(argument, "-s="), false), true
			}
		}
	}
	return "", false
}

func validAINativeSelection(value string, requireUUID bool) string {
	value = strings.TrimSpace(value)
	if !validAINativeSessionID(value) || (requireUUID && !validAIUUID(value)) {
		return ""
	}
	return value
}

func validAIUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

type aiNativeLaunchPreparation struct {
	Args            []string
	Environment     map[string]string
	NativeSessionID string
}

func aiPrepareNativeLaunch(tool string, args []string, homeDir string) (aiNativeLaunchPreparation, error) {
	result := aiNativeLaunchPreparation{Args: append([]string{}, args...), Environment: make(map[string]string)}
	nativeSessionID, selectionPresent := aiRecognizeNativeResume(tool, args)
	result.NativeSessionID = nativeSessionID
	adapter, supported := aiNativeAdapters[tool]
	if !supported {
		return result, nil
	}

	switch adapter.BindingStrategy {
	case "assigned-id":
		if selectionPresent {
			return result, nil
		}
		generatedID, err := newAIUUID()
		if err != nil {
			return aiNativeLaunchPreparation{}, fmt.Errorf("create Copilot native session id: %w", err)
		}
		result.NativeSessionID = generatedID
		result.Args = append([]string{"--session-id=" + generatedID}, result.Args...)
		return result, nil
	case "lifecycle-hook":
		executable, err := aiCurrentExecutable()
		if err != nil {
			return aiNativeLaunchPreparation{}, err
		}
		posixCommand, windowsCommand := aiNativeHookCommands(executable, tool)
		if tool == "codex" {
			if key, conflict := aiCodexLifecycleOverride(args); conflict {
				return aiNativeLaunchPreparation{}, fmt.Errorf("Codex %s cannot be combined with the workgraph native adapter; remove that CLI override and use standard Codex hook configuration instead", key)
			}
			handler := `[{type="command",command=` + strconv.Quote(posixCommand) + `,command_windows=` + strconv.Quote(windowsCommand) + `,timeout=3,statusMessage="Linking workgraph session"}]`
			startConfig := `hooks.SessionStart=[{matcher="^(startup|resume|clear)$",hooks=` + handler + `}]`
			endConfig := `hooks.SessionEnd=[{hooks=` + handler + `}]`
			result.Args = append([]string{"-c", startConfig, "-c", endConfig}, result.Args...)
			return result, nil
		}
		if hasAINativeOption(args, "--settings") {
			return aiNativeLaunchPreparation{}, errors.New("Claude --settings cannot be combined with the workgraph native adapter; use standard Claude settings files instead")
		}
		command := posixCommand
		if runtime.GOOS == "windows" {
			command = windowsCommand
		}
		handler := map[string]any{"type": "command", "command": command, "timeout": 3}
		settings := map[string]any{"hooks": map[string]any{
			"SessionStart": []any{map[string]any{"matcher": "startup|resume|clear|compact|fork", "hooks": []any{handler}}},
			"SessionEnd":   []any{map[string]any{"hooks": []any{handler}}},
		}}
		encoded, err := json.Marshal(settings)
		if err != nil {
			return aiNativeLaunchPreparation{}, fmt.Errorf("encode Claude lifecycle settings: %w", err)
		}
		result.Args = append([]string{"--settings", string(encoded)}, result.Args...)
		return result, nil
	case "opencode-plugin":
		pluginPath, err := ensureAIOpenCodePlugin(homeDir)
		if err != nil {
			return aiNativeLaunchPreparation{}, err
		}
		inlineConfig, err := mergeAIOpenCodeInlineConfig(os.Getenv("OPENCODE_CONFIG_CONTENT"), pluginPath)
		if err != nil {
			return aiNativeLaunchPreparation{}, err
		}
		executable, err := aiCurrentExecutable()
		if err != nil {
			return aiNativeLaunchPreparation{}, err
		}
		hookArgv, err := json.Marshal(aiNativeHookArgv(executable, tool))
		if err != nil {
			return aiNativeLaunchPreparation{}, fmt.Errorf("encode OpenCode hook command: %w", err)
		}
		result.Environment["OPENCODE_CONFIG_CONTENT"] = inlineConfig
		result.Environment["WORKGRAPH_AI_HOOK_ARGV"] = string(hookArgv)
		return result, nil
	default:
		return result, nil
	}
}

func hasAINativeOption(args []string, name string) bool {
	for _, argument := range args {
		if argument == name || strings.HasPrefix(argument, name+"=") {
			return true
		}
	}
	return false
}

func aiCodexLifecycleOverride(args []string) (string, bool) {
	for index := 0; index < len(args); index++ {
		argument := args[index]
		if argument == "--" {
			break
		}
		var override string
		switch {
		case argument == "-c" || argument == "--config":
			if index+1 >= len(args) {
				continue
			}
			index++
			override = args[index]
		case strings.HasPrefix(argument, "-c="):
			override = strings.TrimPrefix(argument, "-c=")
		case strings.HasPrefix(argument, "--config="):
			override = strings.TrimPrefix(argument, "--config=")
		default:
			continue
		}
		key, _, found := strings.Cut(override, "=")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "hooks.SessionStart":
			return "hooks.SessionStart", true
		case "hooks.SessionEnd":
			return "hooks.SessionEnd", true
		}
	}
	return "", false
}

func aiCurrentExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.Abs(executable)
}

func aiNativeHookArgv(executable, tool string) []string {
	result := []string{executable, "__ai-native-session", "--tool", tool}
	if strings.Contains(filepath.ToSlash(executable), "/go-build") {
		if _, sourceFile, _, ok := runtime.Caller(0); ok {
			packagePath := filepath.Join(filepath.Dir(sourceFile), "cmd", "workgraph")
			if _, err := os.Stat(filepath.Join(packagePath, "main.go")); err == nil {
				result = []string{"go", "run", packagePath, "__ai-native-session", "--tool", tool}
			}
		}
	}
	return result
}

func aiNativeHookCommands(executable, tool string) (string, string) {
	argv := aiNativeHookArgv(executable, tool)
	if len(argv) >= 3 && argv[0] == "go" && argv[1] == "run" {
		remainder := strings.Join(argv[3:], " ")
		return "go run " + quoteAIPosixArgument(argv[2]) + " " + remainder,
			"go run " + quoteAIWindowsArgument(argv[2]) + " " + remainder
	}
	remainder := strings.Join(argv[1:], " ")
	return quoteAIPosixArgument(argv[0]) + " " + remainder,
		quoteAIWindowsArgument(argv[0]) + " " + remainder
}

func quoteAIPosixArgument(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func quoteAIWindowsArgument(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

const aiOpenCodePlugin = `export const WorkgraphSessionPlugin = async () => {
  let boundSessionID = ""
  return {
    event: async ({ event }) => {
      if (event?.type !== "session.created" && event?.type !== "session.updated") return
      const info = event?.properties?.info
      if (!info || info.parentID) return
      const sessionID = event?.properties?.sessionID || info.id
      if (typeof sessionID !== "string" || !sessionID || sessionID === boundSessionID) return
      try {
        const argv = JSON.parse(process.env.WORKGRAPH_AI_HOOK_ARGV || "[]")
        if (!Array.isArray(argv) || argv.length === 0 || !argv.every((item) => typeof item === "string")) return
        const payload = JSON.stringify({
          session_id: sessionID,
          cwd: typeof info.directory === "string" ? info.directory : "",
          hook_event_name: "SessionStart",
          source: event.type === "session.created" ? "startup" : "resume"
        })
        const child = Bun.spawn(argv, { env: process.env, stdin: "pipe", stdout: "ignore", stderr: "inherit" })
        child.stdin.write(payload)
        child.stdin.end()
        if (await child.exited === 0) boundSessionID = sessionID
      } catch {
        // Native identity binding is best-effort and must not break OpenCode.
      }
    }
  }
}
`

func ensureAIOpenCodePlugin(homeDir string) (string, error) {
	directory := filepath.Join(homeDir, "ai-adapters")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create OpenCode adapter directory: %w", err)
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return "", fmt.Errorf("inspect OpenCode adapter directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", errors.New("OpenCode adapter directory must be a real directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", fmt.Errorf("secure OpenCode adapter directory: %w", err)
	}

	path := filepath.Join(directory, "workgraph-session.js")
	temporary, err := os.CreateTemp(directory, ".workgraph-session-*")
	if err != nil {
		return "", fmt.Errorf("create OpenCode adapter file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return "", fmt.Errorf("secure OpenCode adapter file: %w", err)
	}
	if _, err := temporary.WriteString(aiOpenCodePlugin); err != nil {
		temporary.Close()
		return "", fmt.Errorf("write OpenCode adapter file: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return "", fmt.Errorf("close OpenCode adapter file: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return "", fmt.Errorf("replace OpenCode adapter file: %w", err)
		}
		if err := os.Rename(temporaryPath, path); err != nil {
			return "", fmt.Errorf("install OpenCode adapter file: %w", err)
		}
	}
	return path, nil
}

func mergeAIOpenCodeInlineConfig(existing, pluginPath string) (string, error) {
	config := make(map[string]any)
	if strings.TrimSpace(existing) != "" {
		if err := json.Unmarshal([]byte(existing), &config); err != nil || config == nil {
			return "", errors.New("OPENCODE_CONFIG_CONTENT must be one valid JSON object for native session binding")
		}
	}
	plugins := []any{}
	if value, present := config["plugin"]; present {
		var ok bool
		plugins, ok = value.([]any)
		if !ok {
			return "", errors.New("OPENCODE_CONFIG_CONTENT field \"plugin\" must be an array for native session binding")
		}
	}
	present := false
	for _, value := range plugins {
		if value == pluginPath {
			present = true
			break
		}
	}
	if !present {
		plugins = append(plugins, pluginPath)
	}
	config["plugin"] = plugins
	encoded, err := json.Marshal(config)
	if err != nil {
		return "", fmt.Errorf("encode OpenCode inline config: %w", err)
	}
	return string(encoded), nil
}

func findLatestAISessionByNativeID(db *sql.DB, tool, nativeSessionID, excludedSessionID string) (string, error) {
	rows, err := db.Query(`SELECT type, timestamp, payload_json FROM events WHERE source = 'ai'`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	type candidate struct {
		tool       string
		nativeIDs  map[string]bool
		latestTime time.Time
	}
	bySession := make(map[string]*candidate)
	for rows.Next() {
		var eventType, timestampText, payloadText string
		if err := rows.Scan(&eventType, &timestampText, &payloadText); err != nil {
			return "", err
		}
		var envelope aiEventEnvelope
		if json.Unmarshal([]byte(payloadText), &envelope) != nil || envelope.SchemaVersion != aiSchemaVersion || envelope.SessionID == "" {
			continue
		}
		entry := bySession[envelope.SessionID]
		if entry == nil {
			entry = &candidate{nativeIDs: make(map[string]bool)}
			bySession[envelope.SessionID] = entry
		}
		if timestamp, err := time.Parse(time.RFC3339Nano, timestampText); err == nil && timestamp.After(entry.latestTime) {
			entry.latestTime = timestamp
		}
		switch eventType {
		case "ai.session_started":
			var started aiStartedPayload
			if json.Unmarshal([]byte(payloadText), &started) == nil {
				entry.tool = started.Tool
				if started.NativeSessionID != "" {
					entry.nativeIDs[started.NativeSessionID] = true
				}
			}
		case "ai.session_native_bound":
			var bound aiNativeBoundPayload
			if json.Unmarshal([]byte(payloadText), &bound) == nil {
				if entry.tool == "" {
					entry.tool = bound.Tool
				}
				entry.nativeIDs[bound.NativeSessionID] = true
			}
		}
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	selected := ""
	var selectedTime time.Time
	for sessionID, entry := range bySession {
		if sessionID == excludedSessionID || entry.tool != tool || !entry.nativeIDs[nativeSessionID] {
			continue
		}
		if selected == "" || entry.latestTime.After(selectedTime) || (entry.latestTime.Equal(selectedTime) && sessionID < selected) {
			selected = sessionID
			selectedTime = entry.latestTime
		}
	}
	return selected, nil
}
