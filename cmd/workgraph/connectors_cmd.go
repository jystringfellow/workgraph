package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func runPlugin(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph plugin <install|doctor>")
		return 2
	}
	switch args[0] {
	case "install":
		return runPluginInstall(args[1:], stdout, stderr)
	case "doctor":
		return runPluginDoctor(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown plugin command: %s\n", args[0])
		return 2
	}
}

func runPluginInstall(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("plugin install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	client := flags.String("client", "", "client to install into: codex, claude-code, copilot, or agy")
	clientCommand := flags.String("client-command", "", "client executable override")
	installRoot := flags.String("install-root", "", "package installation root override")
	noLaunchd := flags.Bool("no-launchd", false, "skip installing the macOS bridge drain worker")
	model := flags.String("model", "", "pin the unattended bridge worker to this client model")
	clearModel := flags.Bool("clear-model", false, "clear the persisted bridge worker model and use the client default")
	configDir := flags.String("config-dir", "", "bind the worker to this signed-in client configuration directory")
	clearConfigDir := flags.Bool("clear-config-dir", false, "clear the persisted client configuration directory binding")
	var providerTools repeatedStringFlags
	flags.Var(&providerTools, "allow-provider-tool", "exact provider MCP tool name to allow for the unattended Claude Code, Copilot, or Antigravity worker; repeatable")
	clearProviderTools := flags.Bool("clear-provider-tools", false, "remove all stored Copilot or Antigravity provider tools before applying --allow-provider-tool")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*client) == "" {
		fmt.Fprintln(stderr, "usage: workgraph plugin install --client <codex|claude-code|copilot|agy>")
		return 2
	}
	result, err := workgraph.InstallPlugin(workgraph.PluginInstallConfig{
		HomeDir: *homeDir, Client: *client, ClientCommand: *clientCommand,
		InstallRoot: *installRoot, SkipLaunchd: *noLaunchd, ProviderTools: providerTools,
		Model: *model, ClearModel: *clearModel, ConfigDir: *configDir, ClearConfigDir: *clearConfigDir,
		ClearProviderTools: *clearProviderTools,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph plugin install: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runPluginDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("plugin doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	client := flags.String("client", "", "client to diagnose: codex, claude-code, copilot, or agy")
	clientCommand := flags.String("client-command", "", "client executable override")
	installRoot := flags.String("install-root", "", "package installation root override")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*client) == "" {
		fmt.Fprintln(stderr, "usage: workgraph plugin doctor --client <codex|claude-code|copilot|agy>")
		return 2
	}
	message, err := workgraph.DoctorPlugin(workgraph.PluginDoctorConfig{
		HomeDir: *homeDir, Client: *client, ClientCommand: *clientCommand, InstallRoot: *installRoot,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph plugin doctor: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, message)
	return 0
}

func runBridge(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph bridge <mcp|install|drain|doctor|login>")
		return 2
	}
	if args[0] == "install" {
		return runBridgeInstall(args[1:], stdout, stderr)
	}
	if args[0] == "drain" {
		return runBridgeDrain(args[1:], stdout, stderr)
	}
	if args[0] == "doctor" {
		return runBridgeDoctor(args[1:], stdout, stderr)
	}
	if args[0] == "login" {
		return runBridgeLogin(args[1:], stdin, stdout, stderr)
	}
	if args[0] != "mcp" {
		fmt.Fprintf(stderr, "unknown bridge command: %s\n", args[0])
		return 2
	}
	mcpArgs := args[1:]
	action := "serve"
	if len(mcpArgs) > 0 && (mcpArgs[0] == "status" || mcpArgs[0] == "stop") {
		action = mcpArgs[0]
		mcpArgs = mcpArgs[1:]
	}
	flags := flag.NewFlagSet("bridge mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	if err := flags.Parse(mcpArgs); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph bridge mcp [status|stop] [--home <path>] [--database <path>] [--memory <path>]")
		return 2
	}
	if action == "status" {
		result, err := workgraph.StatusBridgeMCP(*homeDir)
		if err != nil {
			fmt.Fprintf(stderr, "workgraph bridge mcp status: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	}
	if action == "stop" {
		result, err := workgraph.StopBridgeMCP(*homeDir)
		if err != nil {
			fmt.Fprintf(stderr, "workgraph bridge mcp stop: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := workgraph.ServeBridgeMCP(workgraph.BridgeMCPConfig{HomeDir: *homeDir, DatabasePath: *databasePath, MemoryDir: *memoryDir, Input: stdin, Output: stdout, Context: ctx}); err != nil {
		fmt.Fprintf(stderr, "workgraph bridge mcp: %v\n", err)
		return 1
	}
	return 0
}

func runBridgeDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("bridge doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	client := flags.String("client", "", "reference client: codex, claude-code, copilot, or agy")
	clientCommand := flags.String("client-command", "", "client executable override")
	installRoot := flags.String("install-root", "", "package installation root override")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*client) == "" {
		fmt.Fprintln(stderr, "usage: workgraph bridge doctor --client <codex|claude-code|copilot|agy>")
		return 2
	}
	message, err := workgraph.DoctorBridge(workgraph.BridgeDoctorConfig{
		HomeDir: *homeDir, Client: *client, ClientCommand: *clientCommand, InstallRoot: *installRoot,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph bridge doctor: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, message)
	return 0
}

func runBridgeDrain(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("bridge drain", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	client := flags.String("client", "", "reference client: codex, claude-code, copilot, or agy")
	clientCommand := flags.String("client-command", "", "client executable override")
	timeout := flags.Duration("timeout", workgraph.DefaultBridgeDrainTimeout, "maximum client session runtime")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*client) == "" || *timeout <= 0 {
		fmt.Fprintln(stderr, "usage: workgraph bridge drain --client <codex|claude-code|copilot|agy> [--timeout <duration>]")
		return 2
	}
	message, err := workgraph.DrainBridge(*homeDir, *client, *clientCommand, *timeout)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph bridge drain: %v\n", err)
		return 1
	}
	if message != "" {
		fmt.Fprintln(stdout, message)
	}
	return 0
}

func runBridgeLogin(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("bridge login", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	client := flags.String("client", "", "reference client: codex, claude-code, or copilot")
	clientCommand := flags.String("client-command", "", "client executable override")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*client) == "" {
		fmt.Fprintln(stderr, "usage: workgraph bridge login --client <codex|claude-code|copilot>")
		return 2
	}
	message, err := workgraph.BridgeLogin(workgraph.BridgeLoginConfig{
		HomeDir: *homeDir, Client: *client, ClientCommand: *clientCommand,
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph bridge login: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, message)
	return 0
}

func runBridgeInstall(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("bridge install", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	client := flags.String("client", "", "reference client: codex, claude-code, copilot, or agy")
	clientCommand := flags.String("client-command", "", "client executable override")
	installRoot := flags.String("install-root", "", "package installation root override")
	noLaunchd := flags.Bool("no-launchd", false, "skip installing the macOS drain worker")
	model := flags.String("model", "", "pin the unattended bridge worker to this client model")
	clearModel := flags.Bool("clear-model", false, "clear the persisted bridge worker model and use the client default")
	configDir := flags.String("config-dir", "", "bind the worker to this signed-in client configuration directory")
	clearConfigDir := flags.Bool("clear-config-dir", false, "clear the persisted client configuration directory binding")
	var providerTools repeatedStringFlags
	flags.Var(&providerTools, "allow-provider-tool", "exact provider MCP tool name to allow for the unattended Claude Code, Copilot, or Antigravity worker; repeatable")
	clearProviderTools := flags.Bool("clear-provider-tools", false, "remove all stored Copilot or Antigravity provider tools before applying --allow-provider-tool")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*client) == "" {
		fmt.Fprintln(stderr, "usage: workgraph bridge install --client <codex|claude-code|copilot|agy>")
		return 2
	}
	result, err := workgraph.InstallBridge(workgraph.BridgeInstallConfig{
		HomeDir: *homeDir, Client: *client, ClientCommand: *clientCommand,
		InstallRoot: *installRoot, SkipLaunchd: *noLaunchd, ProviderTools: providerTools,
		Model: *model, ClearModel: *clearModel, ConfigDir: *configDir, ClearConfigDir: *clearConfigDir,
		ClearProviderTools: *clearProviderTools,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph bridge install: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectors(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph connectors <connect|mode|list|required-tools|status|doctor|upgrade|poll|validate|enable|disable|interval>")
		return 2
	}

	switch args[0] {
	case "connect":
		return runConnectorsConnect(args[1:], stdout, stderr)
	case "mode":
		return runConnectorsMode(args[1:], stdout, stderr)
	case "list":
		return runConnectorsList(args[1:], stdout, stderr)
	case "required-tools":
		return runConnectorsRequiredTools(args[1:], stdout, stderr)
	case "status":
		return runConnectorsStatus(args[1:], stdout, stderr)
	case "doctor":
		return runConnectorsDoctor(args[1:], stdout, stderr)
	case "upgrade":
		return runConnectorsUpgrade(args[1:], stdout, stderr)
	case "poll":
		return runConnectorsPoll(args[1:], stdout, stderr)
	case "validate":
		return runConnectorsValidate(args[1:], stdout, stderr)
	case "enable":
		return runConnectorsEnable(args[1:], stdout, stderr)
	case "disable":
		return runConnectorsDisable(args[1:], stdout, stderr)
	case "interval":
		return runConnectorsInterval(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown connectors command: %s\n", args[0])
		return 2
	}
}

func runConnectorsRequiredTools(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors required-tools", flag.ContinueOnError)
	flags.SetOutput(stderr)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: workgraph connectors required-tools [connector]")
		return 2
	}
	connectorID := ""
	if flags.NArg() == 1 {
		connectorID = flags.Arg(0)
	}
	result, err := workgraph.RequiredConnectorTools(connectorID)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors required-tools: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors connect", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	mode := flags.String("mode", "direct", "capture mode: direct or bridged")
	paramsJSON := flags.String("params-json", "", "non-secret bridged connector scope as a JSON object")
	authentication := flags.String("authentication", "", "provider authentication mode for supported bridged connectors")
	az := flags.String("az", "az", "Azure CLI executable for azcli authentication validation")
	connectorArg := ""
	connectorFirst := false
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		connectorArg = args[0]
		connectorFirst = true
		args = args[1:]
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if connectorArg == "" && flags.NArg() == 1 {
		connectorArg = flags.Arg(0)
	}
	if connectorArg == "" || flags.NArg() > 1 || (connectorFirst && flags.NArg() != 0) {
		fmt.Fprintln(stderr, "usage: workgraph connectors connect [--mode direct|bridged] <connector>")
		return 2
	}
	if strings.ToLower(strings.TrimSpace(*mode)) != "bridged" {
		fmt.Fprintln(stderr, "workgraph connectors connect: generic setup currently requires --mode bridged; use the provider-specific connect command for direct capture")
		return 1
	}
	result, err := workgraph.ConfigureBridgedConnector(workgraph.ConnectorBridgeConfig{
		HomeDir:         *homeDir,
		ID:              connectorArg,
		BridgeParams:    json.RawMessage(*paramsJSON),
		Authentication:  *authentication,
		AzureCLICommand: *az,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors connect: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsMode(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors mode", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: workgraph connectors mode <connector> <direct|bridged>")
		return 2
	}
	result, err := workgraph.SetConnectorMode(workgraph.ConnectorModeConfig{
		HomeDir: *homeDir,
		ID:      flags.Arg(0),
		Mode:    flags.Arg(1),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors mode: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runCapture(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph capture <requests|ingest|watermark>")
		return 2
	}
	switch args[0] {
	case "requests":
		return runCaptureRequests(args[1:], stdin, stdout, stderr)
	case "ingest":
		return runCaptureIngest(args[1:], stdin, stdout, stderr)
	case "watermark":
		return runCaptureWatermark(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown capture command: %s\n", args[0])
		return 2
	}
}

func runCaptureRequests(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("capture requests", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	claim := flags.Bool("claim", false, "claim available capture requests")
	list := flags.Bool("list", false, "list capture requests")
	cancelID := flags.String("cancel", "", "cancel one pending or claimed request id")
	cancelReason := flags.String("reason", "", "reason recorded for request cancellation")
	renewID := flags.String("renew", "", "renew a claimed request id")
	failID := flags.String("fail", "", "report failure for a claimed request id")
	errorJSON := flags.String("error-json", "", "JSON failure details {\"error\", optional \"reason\": \"window_too_large\"}; - reads stdin")
	connector := flags.String("connector", "", "limit claims to one connector")
	kind := flags.String("kind", "", "request kind to claim: capture (default) or llm_task")
	memory := flags.String("memory", "", "workgraph memory directory for llm_task claims")
	maxClaims := flags.Int("max", 1, "maximum requests to claim")
	worker := flags.String("worker", "", "bridge worker identity")
	claimFile := flags.String("claim-file", "", "0600 file for the returned claim capability")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph capture requests --list | --cancel <id> [--reason <text>] | --claim [--connector X] [--kind capture|llm_task] [--max N] --worker <name> --claim-file <path>")
		return 2
	}
	if (strings.TrimSpace(*kind) != "" || strings.TrimSpace(*memory) != "") && !*claim {
		fmt.Fprintln(stderr, "workgraph capture requests: --kind and --memory require --claim")
		return 2
	}
	if strings.TrimSpace(*cancelID) != "" {
		if *claim || *list || strings.TrimSpace(*renewID) != "" || strings.TrimSpace(*failID) != "" || strings.TrimSpace(*claimFile) != "" || strings.TrimSpace(*worker) != "" || strings.TrimSpace(*connector) != "" || *maxClaims != 1 {
			fmt.Fprintln(stderr, "usage: workgraph capture requests --cancel <id> [--reason <text>]")
			return 2
		}
		if err := workgraph.CancelCaptureRequest(workgraph.CaptureRequestCancelConfig{
			HomeDir: *homeDir, DatabasePath: *databasePath, RequestID: *cancelID, Reason: *cancelReason,
		}); err != nil {
			fmt.Fprintf(stderr, "workgraph capture requests cancel: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Capture request cancelled\nRequest: %s\n", strings.TrimSpace(*cancelID))
		return 0
	}
	if strings.TrimSpace(*cancelReason) != "" {
		fmt.Fprintln(stderr, "workgraph capture requests: --reason requires --cancel")
		return 2
	}
	capabilityOperation := strings.TrimSpace(*renewID) != "" || strings.TrimSpace(*failID) != ""
	if capabilityOperation {
		if *claim || *list || (strings.TrimSpace(*renewID) != "" && strings.TrimSpace(*failID) != "") || strings.TrimSpace(*claimFile) == "" {
			fmt.Fprintln(stderr, "usage: workgraph capture requests (--renew <id> | --fail <id>) --claim-file <path>")
			return 2
		}
		requestID := strings.TrimSpace(*renewID)
		if requestID == "" {
			requestID = strings.TrimSpace(*failID)
		}
		claimRequestID, claimToken, err := readCaptureClaimFile(*claimFile)
		if err != nil {
			fmt.Fprintf(stderr, "workgraph capture requests: %v\n", err)
			return 1
		}
		if claimRequestID != requestID {
			fmt.Fprintln(stderr, "workgraph capture requests: claim file does not match request")
			return 1
		}
		config := workgraph.CaptureRequestCapabilityConfig{
			HomeDir: *homeDir, DatabasePath: *databasePath, RequestID: requestID, ClaimToken: claimToken,
		}
		if strings.TrimSpace(*renewID) != "" {
			request, err := workgraph.RenewCaptureRequest(config)
			if err != nil {
				fmt.Fprintf(stderr, "workgraph capture requests renew: %v\n", err)
				return 1
			}
			fmt.Fprintf(stdout, "Capture request renewed\nRequest: %s\nLease expires: %s\n", request.ID, request.LeaseExpiresAt)
			return 0
		}
		if *errorJSON != "" {
			if *errorJSON != "-" {
				fmt.Fprintln(stderr, "workgraph capture requests: --error-json currently supports stdin (-) only")
				return 2
			}
			var details struct {
				Error  string `json:"error"`
				Reason string `json:"reason"`
			}
			if err := json.NewDecoder(stdin).Decode(&details); err != nil {
				fmt.Fprintf(stderr, "workgraph capture requests: parse failure JSON: %v\n", err)
				return 1
			}
			config.Error = details.Error
			config.Reason = strings.TrimSpace(details.Reason)
		}
		if err := workgraph.FailCaptureRequest(config); err != nil {
			fmt.Fprintf(stderr, "workgraph capture requests fail: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Capture request returned for retry\nRequest: %s\n", requestID)
		return 0
	}
	if !*claim {
		if strings.TrimSpace(*worker) != "" || strings.TrimSpace(*claimFile) != "" {
			fmt.Fprintln(stderr, "workgraph capture requests: --worker and --claim-file require --claim")
			return 2
		}
		requests, err := workgraph.ListCaptureRequests(workgraph.CaptureRequestListConfig{
			HomeDir: *homeDir, DatabasePath: *databasePath, ConnectorID: *connector,
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph capture requests: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, "Capture requests")
		if len(requests) == 0 {
			fmt.Fprintln(stdout, "No capture requests.")
		}
		for _, request := range requests {
			status := request.Status
			if request.StatusDetail != "" {
				status = request.StatusDetail
			}
			semantics := request.CaptureSemantics
			if request.Kind == "llm_task" {
				semantics = request.Kind
			}
			line := fmt.Sprintf("- %s: %s, connector %s, %s, %s to %s, attempts %d", request.ID, status, request.ConnectorID, semantics, request.Since, request.Until, request.Attempts)
			if request.ClaimedBy != "" {
				line += ", worker " + request.ClaimedBy + ", lease expires " + request.LeaseExpiresAt
			}
			fmt.Fprintln(stdout, line)
		}
		return 0
	}
	if *list {
		fmt.Fprintln(stderr, "workgraph capture requests: choose either --list or --claim")
		return 2
	}
	if strings.TrimSpace(*worker) == "" || strings.TrimSpace(*claimFile) == "" {
		fmt.Fprintln(stderr, "usage: workgraph capture requests --claim [--connector X] [--kind capture|llm_task] [--max N] --worker <name> --claim-file <path>")
		return 2
	}
	if *maxClaims != 1 {
		fmt.Fprintln(stderr, "workgraph capture requests: CLI claim files currently require --max 1")
		return 2
	}
	claimConfig := workgraph.CaptureRequestClaimConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ConnectorID:  *connector,
		Kind:         *kind,
		Worker:       *worker,
		Max:          *maxClaims,
		MemoryDir:    *memory,
	}
	if strings.TrimSpace(*kind) == "llm_task" {
		claimConfig.GitEmails = workgraph.LocalGitEmails(workgraph.LocalGitIdentityConfig{HomeDir: *homeDir})
	}
	claimed, err := workgraph.ClaimCaptureRequests(claimConfig)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph capture requests: %v\n", err)
		return 1
	}
	if len(claimed) == 0 {
		fmt.Fprintln(stdout, "No capture requests available.")
		return 0
	}
	claimContents, err := json.MarshalIndent(struct {
		RequestID  string `json:"request_id"`
		ClaimToken string `json:"claim_token"`
	}{RequestID: claimed[0].Request.ID, ClaimToken: claimed[0].ClaimToken}, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "workgraph capture requests: encode claim file: %v\n", err)
		return 1
	}
	if err := os.WriteFile(*claimFile, append(claimContents, '\n'), 0o600); err != nil {
		fmt.Fprintf(stderr, "workgraph capture requests: write claim file: %v\n", err)
		return 1
	}
	if err := os.Chmod(*claimFile, 0o600); err != nil {
		fmt.Fprintf(stderr, "workgraph capture requests: secure claim file: %v\n", err)
		return 1
	}
	request := claimed[0].Request
	if task := claimed[0].Task; task != nil {
		encodedTask, err := json.Marshal(task)
		if err != nil {
			fmt.Fprintf(stderr, "workgraph capture requests: encode task: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Capture request claimed\nRequest: %s\nKind: %s\nConnector: %s\nParams: %s\nLease expires: %s\nClaim file: %s\nSubmit: workgraph llm associate submit --request %s --claim-file %s\nTask: %s\n",
			request.ID, request.Kind, request.ConnectorID, request.Params, request.LeaseExpiresAt, *claimFile, request.ID, *claimFile, encodedTask)
		return 0
	}
	fmt.Fprintf(stdout, "Capture request claimed\nRequest: %s\nConnector: %s\nSource: %s\nCapture semantics: %s\nSince: %s\nUntil: %s\nParams: %s\nLease expires: %s\nClaim file: %s\n",
		request.ID, request.ConnectorID, request.Source, request.CaptureSemantics, request.Since, request.Until, request.Params, request.LeaseExpiresAt, *claimFile)
	return 0
}

func runCaptureWatermark(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("capture watermark", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	connector := flags.String("connector", "", "connector id")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*connector) == "" {
		fmt.Fprintln(stderr, "usage: workgraph capture watermark --connector <connector>")
		return 2
	}
	watermark, err := workgraph.CaptureWatermark(workgraph.CaptureRequestListConfig{
		HomeDir: *homeDir, DatabasePath: *databasePath, ConnectorID: *connector,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph capture watermark: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, watermark)
	return 0
}

func runCaptureIngest(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("capture ingest", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	source := flags.String("source", "", "connector source id")
	requestID := flags.String("request", "", "claimed capture request id")
	claimFile := flags.String("claim-file", "", "file containing the claim capability")
	jsonInput := flags.String("json", "-", "JSON input path; - reads stdin")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	manual := strings.TrimSpace(*source) != ""
	scheduled := strings.TrimSpace(*requestID) != "" && strings.TrimSpace(*claimFile) != ""
	if flags.NArg() != 0 || manual == scheduled {
		fmt.Fprintln(stderr, "usage: workgraph capture ingest (--source <connector> | --request <id> --claim-file <path>) [--json -]")
		return 2
	}
	if *jsonInput != "-" {
		fmt.Fprintln(stderr, "workgraph capture ingest: --json currently supports stdin (-) only")
		return 1
	}
	claimToken := ""
	if scheduled {
		claimRequestID, token, err := readCaptureClaimFile(*claimFile)
		if err != nil {
			fmt.Fprintf(stderr, "workgraph capture ingest: %v\n", err)
			return 1
		}
		if claimRequestID != *requestID {
			fmt.Fprintln(stderr, "workgraph capture ingest: claim file does not match request")
			return 1
		}
		claimToken = token
	}
	result, err := workgraph.IngestBridgedCapture(workgraph.BridgedIngestConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Source:       *source,
		RequestID:    *requestID,
		ClaimToken:   claimToken,
		Input:        stdin,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph capture ingest: %v\n", err)
		return 1
	}
	if result.WeakDedupe > 0 {
		fmt.Fprintf(stderr, "workgraph capture ingest warning: %d event(s) omitted external_id; weak dedupe was used\n", result.WeakDedupe)
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func readCaptureClaimFile(path string) (string, string, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read claim file: %w", err)
	}
	var claim struct {
		RequestID  string `json:"request_id"`
		ClaimToken string `json:"claim_token"`
	}
	if err := json.Unmarshal(contents, &claim); err != nil {
		return "", "", fmt.Errorf("parse claim file: %w", err)
	}
	if strings.TrimSpace(claim.RequestID) == "" || strings.TrimSpace(claim.ClaimToken) == "" {
		return "", "", fmt.Errorf("claim file is missing request_id or claim_token")
	}
	return claim.RequestID, claim.ClaimToken, nil
}

func runConnectorsList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors list", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	result, err := workgraph.ListConnectors(workgraph.ConnectorListConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors list: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsStatus(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	result, err := workgraph.StatusConnectors(workgraph.ConnectorListConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors status: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	az := flags.String("az", "az", "Azure CLI executable for azcli authentication validation")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph connectors doctor")
		return 2
	}
	result, err := workgraph.DoctorConnectors(workgraph.ConnectorListConfig{
		HomeDir:         *homeDir,
		AzureCLICommand: *az,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors doctor: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsUpgrade(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors upgrade", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph connectors upgrade")
		return 2
	}
	result, err := workgraph.UpgradeConnectors(workgraph.ConnectorListConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors upgrade: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsValidate(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors validate", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	gh := flags.String("gh", "", "gh-compatible executable for GitHub auth validation")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: workgraph connectors validate <connector>")
		return 2
	}
	result, err := workgraph.ValidateConnector(workgraph.ConnectorValidateConfig{
		HomeDir:       *homeDir,
		ID:            flags.Arg(0),
		GitHubCommand: *gh,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors validate: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsPoll(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors poll", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	connector := flags.String("connector", "", "Connector id to poll")
	once := flags.Bool("once", false, "Poll once and exit")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph connectors poll --once [--connector <connector>]")
		return 2
	}
	result, err := workgraph.PollConnectors(workgraph.ConnectorPollConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ID:           *connector,
		Once:         *once,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors poll: %v\n", err)
		if result.Message != "" {
			fmt.Fprintln(stdout, result.Message)
		}
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsEnable(args []string, stdout io.Writer, stderr io.Writer) int {
	return runConnectorsSetEnabled("enable", args, stdout, stderr, true)
}

func runConnectorsDisable(args []string, stdout io.Writer, stderr io.Writer) int {
	return runConnectorsSetEnabled("disable", args, stdout, stderr, false)
}

func runConnectorsSetEnabled(command string, args []string, stdout io.Writer, stderr io.Writer, enabled bool) int {
	flags := flag.NewFlagSet("connectors "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintf(stderr, "usage: workgraph connectors %s <connector>\n", command)
		return 2
	}
	result, err := workgraph.SetConnectorEnabled(workgraph.ConnectorUpdateConfig{
		HomeDir: *homeDir,
		ID:      flags.Arg(0),
		Enabled: enabled,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors %s: %v\n", command, err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConnectorsInterval(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("connectors interval", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 2 {
		fmt.Fprintln(stderr, "usage: workgraph connectors interval <connector> <duration>")
		return 2
	}
	interval, err := time.ParseDuration(flags.Arg(1))
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors interval: parse duration: %v\n", err)
		return 1
	}
	result, err := workgraph.SetConnectorInterval(workgraph.ConnectorUpdateConfig{
		HomeDir:  *homeDir,
		ID:       flags.Arg(0),
		Interval: interval,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph connectors interval: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}
