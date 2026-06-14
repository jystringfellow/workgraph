package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph <command>")
		return 2
	}

	switch args[0] {
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "config":
		return runConfig(args[1:], stdout, stderr)
	case "connectors":
		return runConnectors(args[1:], stdout, stderr)
	case "doctor":
		return runDoctor(args[1:], stdout, stderr)
	case "git":
		return runGit(args[1:], stdout, stderr)
	case "github":
		return runGitHub(args[1:], stdout, stderr)
	case "calendar":
		return runCalendar(args[1:], stdout, stderr)
	case "mail":
		return runMail(args[1:], stdout, stderr)
	case "azure":
		return runAzure(args[1:], stdout, stderr)
	case "llm":
		return runLLM(args[1:], stdout, stderr)
	case "events":
		return runEvents(args[1:], stdout, stderr)
	case "suggestions":
		return runSuggestions(args[1:], stdout, stderr)
	case "notion":
		return runNotion(args[1:], stdout, stderr)
	case "memory":
		return runMemory(args[1:], stdout, stderr)
	case "start":
		return runCaptureStart(args[1:], stdout, stderr)
	case "status":
		return runCaptureStatus(args[1:], stdout, stderr)
	case "stop":
		return runCaptureStop(args[1:], stdout, stderr)
	case "today":
		return runToday(args[1:], stdout, stderr)
	case "resume":
		return runResume(args[1:], stdout, stderr)
	case "slack":
		return runSlack(args[1:], stdout, stderr)
	case "__capture-worker":
		return runCaptureWorker(args[1:], stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}

func runSuggestions(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions <list|dismiss>")
		return 2
	}
	switch args[0] {
	case "list":
		return runSuggestionsList(args[1:], stdout, stderr)
	case "dismiss":
		return runSuggestionsDismiss(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown suggestions command: %s\n", args[0])
		return 2
	}
}

func runSuggestionsList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("suggestions list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	status := flags.String("status", "", "Suggestion status to include")
	limit := flags.Int("limit", 25, "Maximum number of suggestions to show")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions list")
		return 2
	}

	result, err := workgraph.ListSuggestions(workgraph.SuggestionListConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Status:       *status,
		Limit:        *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions list: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSuggestionsDismiss(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions dismiss <id> --reason <code>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("suggestions dismiss "+id, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	reason := flags.String("reason", "", "Stable dismiss reason code")
	note := flags.String("note", "", "Optional dismiss note")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph suggestions dismiss <id> --reason <code>")
		return 2
	}

	suggestion, err := workgraph.DismissSuggestion(workgraph.SuggestionStatusUpdate{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ID:           id,
		ReasonCode:   *reason,
		FeedbackNote: *note,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph suggestions dismiss: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Suggestion dismissed\nid: %s\nstatus: %s\n", suggestion.ID, suggestion.Status)
	return 0
}

func runDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph doctor")
		return 2
	}

	result, err := workgraph.Doctor(workgraph.DoctorConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph doctor: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runEvents(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph events <today>")
		return 2
	}
	switch args[0] {
	case "today":
		return runEventsToday(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown events command: %s\n", args[0])
		return 2
	}
}

func runEventsToday(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("events today", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	eventType := flags.String("type", "", "Event type to include")
	limit := flags.Int("limit", 0, "Maximum number of matching events to show")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.EventsToday(workgraph.EventsTodayConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Type:         *eventType,
		Limit:        *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph events today: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLM(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm <command>")
		return 2
	}

	switch args[0] {
	case "add":
		return runLLMAdd(args[1:], stdout, stderr)
	case "list":
		return runLLMList(args[1:], stdout, stderr)
	case "remove":
		return runLLMRemove(args[1:], stdout, stderr)
	case "use":
		return runLLMUse(args[1:], stdout, stderr)
	case "test":
		return runLLMTest(args[1:], stdout, stderr)
	case "summarize":
		return runLLMSummarize(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown llm command: %s\n", args[0])
		return 2
	}
}

func runLLMAdd(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm add <profile>")
		return 2
	}
	profile := args[0]

	flags := flag.NewFlagSet("llm add "+profile, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	provider := flags.String("provider", "", "LLM provider")
	baseURL := flags.String("base-url", "", "OpenAI-compatible base URL")
	model := flags.String("model", "", "LLM model")
	apiKeyEnv := flags.String("api-key-env", "", "Environment variable containing the API key")
	awsProfile := flags.String("aws-profile", "", "AWS profile for Bedrock")
	region := flags.String("region", "", "Cloud provider region")
	modelARN := flags.String("model-arn", "", "Bedrock model or inference profile ARN")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	result, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
		HomeDir:    *homeDir,
		Name:       profile,
		Provider:   *provider,
		BaseURL:    *baseURL,
		Model:      *model,
		APIKeyEnv:  *apiKeyEnv,
		AWSProfile: *awsProfile,
		Region:     *region,
		ModelARN:   *modelARN,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm add: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.ListLLMProfiles(workgraph.LLMListConfig{HomeDir: *homeDir})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm list: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMRemove(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm remove <profile>")
		return 2
	}
	profile := args[0]

	flags := flag.NewFlagSet("llm remove "+profile, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	result, err := workgraph.RemoveLLMProfile(workgraph.LLMRemoveProfileConfig{
		HomeDir: *homeDir,
		Name:    profile,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm remove: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMUse(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm use <profile>")
		return 2
	}
	profile := args[0]

	flags := flag.NewFlagSet("llm use "+profile, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	task := flags.String("for", "", "Task to route to this profile")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	result, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{
		HomeDir: *homeDir,
		Name:    profile,
		Task:    *task,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm use: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMTest(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm test", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	profile := flags.String("profile", "", "LLM profile to test")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.TestLLMProfile(workgraph.LLMTestConfig{
		HomeDir: *homeDir,
		Profile: *profile,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm test: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMSummarize(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm summarize <target>")
		return 2
	}
	target := args[0]

	flags := flag.NewFlagSet("llm summarize "+target, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	dryRun := flags.Bool("dry-run", false, "Preview prompt and context without calling the provider")
	noStream := flags.Bool("no-stream", false, "Print the summary after the provider call completes")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if target != "today" {
		fmt.Fprintf(stderr, "unsupported llm summarize target: %s\n", target)
		return 2
	}

	summarizeConfig := workgraph.LLMSummarizeTodayConfig{
		HomeDir: *homeDir,
		DryRun:  *dryRun,
	}
	if !*dryRun && !*noStream {
		summarizeConfig.Stream = func(chunk string) error {
			_, err := fmt.Fprint(stdout, chunk)
			return err
		}
	}
	result, err := workgraph.SummarizeTodayWithLLM(summarizeConfig)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm summarize today: %v\n", err)
		return 1
	}
	if result.Message != "" {
		fmt.Fprintln(stdout, result.Message)
	}
	return 0
}

func runConnectors(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph connectors <list|status|enable|disable|interval>")
		return 2
	}

	switch args[0] {
	case "list":
		return runConnectorsList(args[1:], stdout, stderr)
	case "status":
		return runConnectorsStatus(args[1:], stdout, stderr)
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

func runNotion(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph notion <command>")
		return 2
	}

	switch args[0] {
	case "capture":
		return runNotionCapture(args[1:], stdout, stderr)
	case "connect":
		return runNotionConnect(args[1:], stdout, stderr)
	case "disconnect":
		return runNotionDisconnect(args[1:], stdout, stderr)
	case "index":
		return runNotionIndex(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown notion command: %s\n", args[0])
		return 2
	}
}

func runNotionIndex(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph notion index <list|show>")
		return 2
	}
	switch args[0] {
	case "list":
		return runNotionIndexList(args[1:], stdout, stderr)
	case "show":
		return runNotionIndexShow(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown notion index command: %s\n", args[0])
		return 2
	}
}

func runNotionIndexList(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("notion index list", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	limit := flags.Int("limit", 25, "Maximum number of indexed Notion objects to show")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	result, err := workgraph.ListNotionIndex(workgraph.NotionIndexListConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Limit:        *limit,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph notion index list: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runNotionIndexShow(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph notion index show <notion-id>")
		return 2
	}
	id := args[0]
	flags := flag.NewFlagSet("notion index show", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	result, err := workgraph.ShowNotionIndex(workgraph.NotionIndexShowConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		ID:           id,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph notion index show: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runNotionCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("notion capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	token := flags.String("token", os.Getenv("WORKGRAPH_NOTION_TOKEN"), "Notion access token")
	notionAPIBaseURL := flags.String("notion-api-base", "", "Notion API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureNotion(workgraph.NotionCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Token:        *token,
		APIBaseURL:   *notionAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph notion capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runNotionConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("notion connect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	clientID := flags.String("client-id", "", "Notion OAuth client id")
	redirectURI := flags.String("redirect-uri", "", "Notion OAuth redirect URI")
	code := flags.String("code", "", "Notion OAuth code")
	state := flags.String("state", "", "Notion OAuth state")
	expectedState := flags.String("expected-state", "", "Expected Notion OAuth state")
	noBrowser := flags.Bool("no-browser", false, "Print the authorization URL instead of opening a browser")
	authBaseURL := flags.String("notion-auth-base", "", "Notion authorization URL")
	tokenURL := flags.String("notion-token-url", "", "Notion token relay URL")
	notionAPIBaseURL := flags.String("notion-api-base", "", "Notion API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	config := workgraph.NotionConnectConfig{
		HomeDir:       *homeDir,
		ClientID:      *clientID,
		RedirectURI:   *redirectURI,
		Code:          *code,
		State:         *state,
		ExpectedState: *expectedState,
		AuthBaseURL:   *authBaseURL,
		TokenURL:      *tokenURL,
		APIBaseURL:    *notionAPIBaseURL,
	}
	var result workgraph.NotionConnectResult
	var err error
	if *code == "" && !*noBrowser {
		result, err = workgraph.ConnectNotionWithBrowser(context.Background(), config)
	} else {
		result, err = workgraph.ConnectNotion(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph notion connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runNotionDisconnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("notion disconnect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.DisconnectNotion(workgraph.NotionDisconnectConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph notion disconnect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMail(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph mail <command>")
		return 2
	}

	switch args[0] {
	case "capture":
		return runMailCapture(args[1:], stdout, stderr)
	case "connect":
		return runMailConnect(args[1:], stdout, stderr)
	case "disconnect":
		return runMailDisconnect(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown mail command: %s\n", args[0])
		return 2
	}
}

func runMailCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("mail capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	provider := flags.String("provider", "", "Mail provider to capture from")
	mailboxID := flags.String("mailbox-id", "", "Provider mailbox id")
	token := flags.String("token", os.Getenv("WORKGRAPH_MAIL_TOKEN"), "Mail provider access token")
	clientID := flags.String("client-id", "", "Mail provider OAuth client id")
	tokenURL := flags.String("mail-token-url", "", "Mail provider token URL")
	mailAPIBaseURL := flags.String("mail-api-base", "", "Mail API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureMailMessages(workgraph.MailCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Provider:     *provider,
		MailboxID:    *mailboxID,
		Token:        *token,
		ClientID:     *clientID,
		TokenURL:     *tokenURL,
		APIBaseURL:   *mailAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph mail capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMailConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph mail connect <provider>")
		return 2
	}
	provider := args[0]

	flags := flag.NewFlagSet("mail connect "+provider, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	clientID := flags.String("client-id", "", "Mail provider OAuth client id")
	redirectURI := flags.String("redirect-uri", "", "Mail provider OAuth redirect URI")
	code := flags.String("code", "", "Mail provider OAuth code")
	codeVerifier := flags.String("code-verifier", "", "Mail provider OAuth PKCE code verifier")
	state := flags.String("state", "", "Mail provider OAuth state")
	expectedState := flags.String("expected-state", "", "Expected Mail provider OAuth state")
	noBrowser := flags.Bool("no-browser", false, "Print the authorization URL instead of opening a browser")
	authBaseURL := flags.String("mail-auth-base", "", "Mail provider authorization URL")
	tokenURL := flags.String("mail-token-url", "", "Mail provider token URL")
	mailAPIBaseURL := flags.String("mail-api-base", "", "Mail API base URL")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	config := workgraph.MailConnectConfig{
		HomeDir:       *homeDir,
		Provider:      provider,
		ClientID:      *clientID,
		RedirectURI:   *redirectURI,
		Code:          *code,
		CodeVerifier:  *codeVerifier,
		State:         *state,
		ExpectedState: *expectedState,
		AuthBaseURL:   *authBaseURL,
		TokenURL:      *tokenURL,
		APIBaseURL:    *mailAPIBaseURL,
	}
	var result workgraph.MailConnectResult
	var err error
	if *code == "" && !*noBrowser {
		result, err = workgraph.ConnectMailWithBrowser(context.Background(), config)
	} else {
		result, err = workgraph.ConnectMail(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph mail connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMailDisconnect(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph mail disconnect <provider>")
		return 2
	}
	provider := args[0]

	flags := flag.NewFlagSet("mail disconnect "+provider, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	revokeURL := flags.String("mail-revoke-url", "", "Mail provider token revoke URL")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	result, err := workgraph.DisconnectMail(workgraph.MailDisconnectConfig{
		HomeDir:   *homeDir,
		Provider:  provider,
		RevokeURL: *revokeURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph mail disconnect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runInit(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	force := flags.Bool("force", false, "Refresh init-owned defaults such as config.json")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.Init(workgraph.InitConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
		Force:        *force,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph init: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runCalendar(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph calendar <command>")
		return 2
	}

	switch args[0] {
	case "capture":
		return runCalendarCapture(args[1:], stdout, stderr)
	case "connect":
		return runCalendarConnect(args[1:], stdout, stderr)
	case "disconnect":
		return runCalendarDisconnect(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown calendar command: %s\n", args[0])
		return 2
	}
}

func runCalendarConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph calendar connect <provider>")
		return 2
	}
	provider := args[0]

	flags := flag.NewFlagSet("calendar connect "+provider, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	clientID := flags.String("client-id", "", "Calendar provider OAuth client id")
	redirectURI := flags.String("redirect-uri", "", "Calendar provider OAuth redirect URI")
	code := flags.String("code", "", "Calendar provider OAuth code")
	codeVerifier := flags.String("code-verifier", "", "Calendar provider OAuth PKCE code verifier")
	state := flags.String("state", "", "Calendar provider OAuth state")
	expectedState := flags.String("expected-state", "", "Expected Calendar provider OAuth state")
	noBrowser := flags.Bool("no-browser", false, "Print the authorization URL instead of opening a browser")
	calendarIDs := watchDirFlags{}
	flags.Var(&calendarIDs, "calendar-id", "Provider calendar id to collect after connecting")
	authBaseURL := flags.String("calendar-auth-base", "", "Calendar provider authorization URL")
	tokenURL := flags.String("calendar-token-url", "", "Calendar provider token URL")
	calendarAPIBaseURL := flags.String("calendar-api-base", "", "Calendar API base URL")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	config := workgraph.CalendarConnectConfig{
		HomeDir:       *homeDir,
		Provider:      provider,
		ClientID:      *clientID,
		RedirectURI:   *redirectURI,
		Code:          *code,
		CodeVerifier:  *codeVerifier,
		State:         *state,
		ExpectedState: *expectedState,
		CalendarIDs:   calendarIDs,
		AuthBaseURL:   *authBaseURL,
		TokenURL:      *tokenURL,
		APIBaseURL:    *calendarAPIBaseURL,
	}
	var result workgraph.CalendarConnectResult
	var err error
	if *code == "" && !*noBrowser {
		result, err = workgraph.ConnectCalendarWithBrowser(context.Background(), config)
	} else {
		result, err = workgraph.ConnectCalendar(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph calendar connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runCalendarDisconnect(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph calendar disconnect <provider>")
		return 2
	}
	provider := args[0]

	flags := flag.NewFlagSet("calendar disconnect "+provider, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	revokeURL := flags.String("calendar-revoke-url", "", "Calendar provider token revoke URL")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}

	result, err := workgraph.DisconnectCalendar(workgraph.CalendarDisconnectConfig{
		HomeDir:   *homeDir,
		Provider:  provider,
		RevokeURL: *revokeURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph calendar disconnect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runCalendarCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("calendar capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	eventsFile := flags.String("events-file", "", "Calendar event export JSON file")
	provider := flags.String("provider", "", "Calendar provider to capture from")
	calendarID := flags.String("calendar-id", "", "Provider calendar id")
	token := flags.String("token", os.Getenv("WORKGRAPH_CALENDAR_TOKEN"), "Calendar provider access token")
	clientID := flags.String("client-id", os.Getenv("WORKGRAPH_GOOGLE_CLIENT_ID"), "Calendar provider OAuth client id")
	tokenURL := flags.String("calendar-token-url", "", "Calendar provider token URL")
	calendarAPIBaseURL := flags.String("calendar-api-base", "", "Calendar API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureCalendarEvents(workgraph.CalendarCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		EventsFile:   *eventsFile,
		Provider:     *provider,
		CalendarID:   *calendarID,
		Token:        *token,
		ClientID:     *clientID,
		TokenURL:     *tokenURL,
		APIBaseURL:   *calendarAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph calendar capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSlack(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph slack <command>")
		return 2
	}

	switch args[0] {
	case "capture":
		return runSlackCapture(args[1:], stdout, stderr)
	case "connect":
		return runSlackConnect(args[1:], stdout, stderr)
	case "disconnect":
		return runSlackDisconnect(args[1:], stdout, stderr)
	case "lists":
		return runSlackLists(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown slack command: %s\n", args[0])
		return 2
	}
}

func runAzure(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph azure <boards>")
		return 2
	}
	switch args[0] {
	case "boards":
		return runAzureBoards(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown azure command: %s\n", args[0])
		return 2
	}
}

func runAzureBoards(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph azure boards <connect|capture|disconnect>")
		return 2
	}
	switch args[0] {
	case "connect":
		return runAzureBoardsConnect(args[1:], stdout, stderr)
	case "capture":
		return runAzureBoardsCapture(args[1:], stdout, stderr)
	case "disconnect":
		return runAzureBoardsDisconnect(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown azure boards command: %s\n", args[0])
		return 2
	}
}

func runAzureBoardsConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("azure boards connect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	clientID := flags.String("client-id", os.Getenv("WORKGRAPH_AZURE_DEVOPS_CLIENT_ID"), "Azure DevOps OAuth client id")
	redirectURI := flags.String("redirect-uri", workgraph.DefaultAzureBoardsRedirectURI, "Azure Boards OAuth redirect URI")
	code := flags.String("code", "", "Azure Boards OAuth code returned to the redirect URI")
	codeVerifier := flags.String("code-verifier", "", "Azure Boards OAuth PKCE verifier")
	state := flags.String("state", "", "Azure Boards OAuth state")
	expectedState := flags.String("expected-state", "", "Expected Azure Boards OAuth state")
	organization := flags.String("organization", "", "Azure DevOps organization name")
	project := flags.String("project", "", "Azure DevOps project name")
	team := flags.String("team", "", "Azure DevOps team name")
	wiql := flags.String("wiql", "", "Custom Azure Boards WIQL query")
	authBaseURL := flags.String("azure-auth-base", "", "Azure Boards OAuth authorization base URL")
	tokenURL := flags.String("azure-token-url", "", "Azure Boards OAuth token URL")
	apiBaseURL := flags.String("azure-api-base", "", "Azure DevOps API base URL")
	noBrowser := flags.Bool("no-browser", false, "Print the OAuth URL instead of opening a browser")
	areaPaths := watchDirFlags{}
	flags.Var(&areaPaths, "area-path", "Azure Boards area path to include")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	config := workgraph.AzureBoardsConnectConfig{
		HomeDir:       *homeDir,
		ClientID:      *clientID,
		RedirectURI:   *redirectURI,
		Code:          *code,
		CodeVerifier:  *codeVerifier,
		State:         *state,
		ExpectedState: *expectedState,
		Organization:  *organization,
		Project:       *project,
		Team:          *team,
		AreaPaths:     areaPaths,
		WIQL:          *wiql,
		AuthBaseURL:   *authBaseURL,
		TokenURL:      *tokenURL,
		APIBaseURL:    *apiBaseURL,
	}
	var result workgraph.AzureBoardsResult
	var err error
	if *code == "" && !*noBrowser {
		result, err = workgraph.ConnectAzureBoardsWithBrowser(context.Background(), config)
	} else {
		result, err = workgraph.ConnectAzureBoards(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph azure boards connect: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runAzureBoardsCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("azure boards capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	token := flags.String("token", os.Getenv("WORKGRAPH_AZURE_DEVOPS_TOKEN"), "Azure DevOps access token")
	organization := flags.String("organization", "", "Azure DevOps organization name")
	project := flags.String("project", "", "Azure DevOps project name")
	team := flags.String("team", "", "Azure DevOps team name")
	wiql := flags.String("wiql", "", "Custom Azure Boards WIQL query")
	apiBaseURL := flags.String("azure-api-base", "", "Azure DevOps API base URL")
	areaPaths := watchDirFlags{}
	flags.Var(&areaPaths, "area-path", "Azure Boards area path to include")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureAzureBoards(workgraph.AzureBoardsCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Token:        *token,
		Organization: *organization,
		Project:      *project,
		Team:         *team,
		AreaPaths:    areaPaths,
		WIQL:         *wiql,
		APIBaseURL:   *apiBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph azure boards capture: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runAzureBoardsDisconnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("azure boards disconnect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.DisconnectAzureBoards(workgraph.AzureBoardsCaptureConfig{HomeDir: *homeDir})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph azure boards disconnect: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSlackDisconnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("slack disconnect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	slackAPIBaseURL := flags.String("slack-api-base", "", "Slack API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.DisconnectSlack(workgraph.SlackDisconnectConfig{
		HomeDir:    *homeDir,
		APIBaseURL: *slackAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph slack disconnect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSlackCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("slack capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	eventsFile := flags.String("events-file", "", "Slack event export JSON file")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureSlackEvents(workgraph.SlackCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		EventsFile:   *eventsFile,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph slack capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSlackLists(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph slack lists <capture>")
		return 2
	}
	switch args[0] {
	case "capture":
		return runSlackListsCapture(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown slack lists command: %s\n", args[0])
		return 2
	}
}

func runSlackListsCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("slack lists capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	token := flags.String("token", os.Getenv("WORKGRAPH_SLACK_TOKEN"), "Slack API token")
	listID := flags.String("list-id", "", "Slack List id to capture")
	slackAPIBaseURL := flags.String("slack-api-base", "", "Slack API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureSlackList(workgraph.SlackListCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Token:        *token,
		ListID:       *listID,
		APIBaseURL:   *slackAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph slack lists capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSlackConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("slack connect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	clientID := flags.String("client-id", os.Getenv("WORKGRAPH_SLACK_CLIENT_ID"), "Slack app client id")
	clientSecret := flags.String("client-secret", os.Getenv("WORKGRAPH_SLACK_CLIENT_SECRET"), "Slack app client secret")
	redirectURI := flags.String("redirect-uri", workgraph.DefaultSlackRedirectURI, "Slack OAuth redirect URI")
	localCallbackURI := flags.String("local-callback-uri", workgraph.DefaultSlackLocalCallbackURI, "Local Slack OAuth callback URI")
	code := flags.String("code", "", "Slack OAuth code returned to the redirect URI")
	state := flags.String("state", "", "Slack OAuth state returned to the redirect URI")
	slackAPIBaseURL := flags.String("slack-api-base", "", "Slack API base URL")
	channels := watchDirFlags{}
	flags.Var(&channels, "channel", "Slack channel id to collect after connecting")
	listIDs := watchDirFlags{}
	flags.Var(&listIDs, "list", "Slack List id to collect after connecting")
	includeDMs := flags.Bool("include-dms", false, "Opt into collecting Slack direct and group direct messages")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	config := workgraph.SlackConnectConfig{
		HomeDir:          *homeDir,
		ClientID:         *clientID,
		ClientSecret:     *clientSecret,
		RedirectURI:      *redirectURI,
		LocalCallbackURI: *localCallbackURI,
		Code:             *code,
		State:            *state,
		ExpectedState:    *state,
		Channels:         channels,
		ListIDs:          listIDs,
		IncludeDMs:       *includeDMs,
		APIBaseURL:       *slackAPIBaseURL,
	}
	var result workgraph.SlackConnectResult
	var err error
	if *code == "" {
		result, err = workgraph.ConnectSlackWithBrowser(context.Background(), config)
	} else {
		result, err = workgraph.ConnectSlack(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph slack connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runGitHub(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph github <command>")
		return 2
	}

	switch args[0] {
	case "connect":
		return runGitHubConnect(args[1:], stdout, stderr)
	case "capture":
		return runGitHubCapture(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown github command: %s\n", args[0])
		return 2
	}
}

func runGitHubConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("github connect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	gh := flags.String("gh", "", "gh-compatible executable for GitHub auth validation")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph github connect")
		return 2
	}

	result, err := workgraph.ConnectGitHub(workgraph.ConnectorConnectConfig{
		HomeDir:       *homeDir,
		GitHubCommand: *gh,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph github connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runGitHubCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("github capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	eventsFile := flags.String("events-file", "", "GitHub event export JSON file")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureGitHubEvents(workgraph.GitHubCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		EventsFile:   *eventsFile,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph github capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runGit(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph git <command>")
		return 2
	}

	switch args[0] {
	case "connect":
		return runGitConnect(args[1:], stdout, stderr)
	case "capture":
		return runGitCapture(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown git command: %s\n", args[0])
		return 2
	}
}

func runGitConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("git connect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph git connect")
		return 2
	}

	result, err := workgraph.ConnectGit(workgraph.ConnectorConnectConfig{
		HomeDir: *homeDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph git connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMemory(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph memory <command>")
		return 2
	}

	switch args[0] {
	case "init":
		return runMemoryInit(args[1:], stdout, stderr)
	case "links":
		return runMemoryLinks(args[1:], stdout, stderr)
	case "promote":
		return runMemoryPromote(args[1:], stdout, stderr)
	case "suggest":
		return runMemorySuggest(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown memory command: %s\n", args[0])
		return 2
	}
}

func runMemoryInit(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory init", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	scope := flags.String("scope", "project", "Memory scope: project, personal, organization, or team")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	switch *scope {
	case "personal":
		if flags.NArg() != 0 {
			fmt.Fprintln(stderr, "usage: workgraph memory init [--home path] [--memory path] --scope personal")
			return 2
		}
		result, err := workgraph.InitPersonalMemory(workgraph.PersonalMemoryInitConfig{
			HomeDir:   *homeDir,
			MemoryDir: *memoryDir,
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory init: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	case "organization":
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: workgraph memory init [--home path] [--memory path] --scope organization <organization>")
			return 2
		}
		result, err := workgraph.InitOrganizationMemory(workgraph.OrganizationMemoryInitConfig{
			HomeDir:      *homeDir,
			MemoryDir:    *memoryDir,
			Organization: flags.Arg(0),
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory init: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	case "team":
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: workgraph memory init [--home path] [--memory path] --scope team <team>")
			return 2
		}
		result, err := workgraph.InitTeamMemory(workgraph.TeamMemoryInitConfig{
			HomeDir:   *homeDir,
			MemoryDir: *memoryDir,
			Team:      flags.Arg(0),
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory init: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	case "project":
		if flags.NArg() != 1 {
			fmt.Fprintln(stderr, "usage: workgraph memory init [--home path] [--memory path] [--scope project] <project>")
			return 2
		}
		result, err := workgraph.InitProjectMemory(workgraph.ProjectMemoryInitConfig{
			HomeDir:   *homeDir,
			MemoryDir: *memoryDir,
			Project:   flags.Arg(0),
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph memory init: %v\n", err)
			return 1
		}
		fmt.Fprintln(stdout, result.Message)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown memory scope: %s\n", *scope)
		return 2
	}
}

func runMemorySuggest(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory suggest", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	scope := flags.String("scope", "project", "Memory suggestion scope")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *scope != "project" || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: workgraph memory suggest [--home path] [--memory path] [--database path] --scope project <project>")
		return 2
	}

	result, err := workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
		Scope:        *scope,
		Project:      flags.Arg(0),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph memory suggest: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMemoryLinks(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory links", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	scope := flags.String("scope", "project", "Memory link scope")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *scope != "project" || flags.NArg() != 1 {
		fmt.Fprintln(stderr, "usage: workgraph memory links [--home path] [--memory path] [--database path] --scope project <project>")
		return 2
	}

	result, err := workgraph.ListMemoryLinks(workgraph.MemoryLinksConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
		Scope:        *scope,
		Project:      flags.Arg(0),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph memory links: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runMemoryPromote(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("memory promote", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	scope := flags.String("scope", "project", "Memory promotion scope")
	evidenceID := flags.String("evidence", "", "Event id supporting the promoted memory")
	text := flags.String("text", "", "Curated memory text to promote")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *scope != "project" || flags.NArg() != 1 || *evidenceID == "" || *text == "" {
		fmt.Fprintln(stderr, "usage: workgraph memory promote [--home path] [--memory path] [--database path] --scope project --evidence event-id --text text <project>")
		return 2
	}

	result, err := workgraph.PromoteMemory(workgraph.MemoryPromoteConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
		Scope:        *scope,
		Project:      flags.Arg(0),
		EvidenceID:   *evidenceID,
		Text:         *text,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph memory promote: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runGitCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("git capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	maxCommits := flags.Int("max-commits", 50, "Maximum recent commits to read per repository")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.CaptureGitCommits(workgraph.GitCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MaxCommits:   *maxCommits,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph git capture: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runConfig(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph config <command>")
		return 2
	}

	switch args[0] {
	case "add-watch":
		return runConfigAddWatch(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown config command: %s\n", args[0])
		return 2
	}
}

func runConfigAddWatch(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("config add-watch", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	path := "."
	if flags.NArg() > 0 {
		path = flags.Arg(0)
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: workgraph config add-watch [path]")
		return 2
	}

	result, err := workgraph.AddWatchDir(workgraph.ConfigWatchConfig{
		HomeDir: *homeDir,
		Path:    path,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph config add-watch: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runCaptureStart(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("start", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	foreground := flags.Bool("foreground", false, "Run capture attached to the current terminal")
	watchDirs := watchDirFlags{}
	flags.Var(&watchDirs, "watch", "Directory to watch for local work activity")
	slackToken := flags.String("slack-token", os.Getenv("WORKGRAPH_SLACK_TOKEN"), "Slack API token for read-only message collection")
	slackAPIBaseURL := flags.String("slack-api-base", "", "Slack API base URL")
	slackChannels := watchDirFlags{}
	flags.Var(&slackChannels, "slack-channel", "Slack channel id to collect while running")
	slackListIDs := watchDirFlags{}
	flags.Var(&slackListIDs, "slack-list", "Slack List id to collect while running")
	slackIncludeDMs := flags.Bool("slack-include-dms", false, "Opt into collecting Slack direct and group direct messages")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	if !*foreground {
		status, err := workgraph.StartDaemon(workgraph.DaemonConfig{
			HomeDir:         *homeDir,
			DatabasePath:    *databasePath,
			WatchDirs:       watchDirs,
			SlackToken:      *slackToken,
			SlackChannels:   slackChannels,
			SlackListIDs:    slackListIDs,
			SlackIncludeDMs: *slackIncludeDMs,
			SlackAPIBaseURL: *slackAPIBaseURL,
		})
		if err != nil {
			fmt.Fprintf(stderr, "workgraph start: %v\n", err)
			return 1
		}

		fmt.Fprintln(stdout, status.Message)
		return 0
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	capture, err := workgraph.StartRun(workgraph.RunConfig{
		HomeDir:         *homeDir,
		DatabasePath:    *databasePath,
		WatchDirs:       watchDirs,
		SlackToken:      *slackToken,
		SlackChannels:   slackChannels,
		SlackListIDs:    slackListIDs,
		SlackIncludeDMs: *slackIncludeDMs,
		SlackAPIBaseURL: *slackAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph start: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, capture.Status.Message)

	eventDone := make(chan struct{})
	go func() {
		defer close(eventDone)
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-capture.Events:
				if !ok {
					return
				}
				fmt.Fprintln(stdout, formatCapturedEvent(event))
			}
		}
	}()

	if err := capture.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "workgraph start: %v\n", err)
		return 1
	}
	<-eventDone

	return 0
}

func formatCapturedEvent(event workgraph.CapturedEvent) string {
	if event.Type == "git.commit" {
		if event.Project != "" && event.Summary != "" {
			return fmt.Sprintf("%s %s %s", event.Type, event.Project, event.Summary)
		}
		if event.Summary != "" {
			return fmt.Sprintf("%s %s", event.Type, event.Summary)
		}
	}
	return fmt.Sprintf("%s %s", event.Type, event.Path)
}

func runCaptureStatus(args []string, stdout io.Writer, stderr io.Writer) int {
	config, ok := parseCaptureControlConfig("status", args, stderr)
	if !ok {
		return 2
	}

	status, err := workgraph.DaemonStatusForConfig(config)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph status: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, status.Message)
	return 0
}

func runCaptureStop(args []string, stdout io.Writer, stderr io.Writer) int {
	config, ok := parseCaptureControlConfig("stop", args, stderr)
	if !ok {
		return 2
	}

	status, err := workgraph.StopDaemon(config)
	if err != nil {
		fmt.Fprintf(stderr, "workgraph stop: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, status.Message)
	return 0
}

func runToday(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("today", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	result, err := workgraph.Today(workgraph.TodayConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph today: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runResume(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("resume", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	memoryDir := flags.String("memory", "", "workgraph memory directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(stderr, "usage: workgraph resume [project]")
		return 2
	}

	project := ""
	if flags.NArg() == 1 {
		project = flags.Arg(0)
	}

	result, err := workgraph.Resume(workgraph.ResumeConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		MemoryDir:    *memoryDir,
		Project:      project,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph resume: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runCaptureWorker(args []string, stderr io.Writer) int {
	config, ok := parseCaptureControlConfig("__capture-worker", args, stderr)
	if !ok {
		return 2
	}

	if err := workgraph.RunDaemon(config); err != nil {
		fmt.Fprintf(stderr, "workgraph capture worker: %v\n", err)
		return 1
	}
	return 0
}

func parseCaptureControlConfig(command string, args []string, stderr io.Writer) (workgraph.DaemonConfig, bool) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	watchDirs := watchDirFlags{}
	flags.Var(&watchDirs, "watch", "Directory to watch for local work activity")
	slackToken := flags.String("slack-token", os.Getenv("WORKGRAPH_SLACK_TOKEN"), "Slack API token for read-only message collection")
	slackAPIBaseURL := flags.String("slack-api-base", "", "Slack API base URL")
	slackChannels := watchDirFlags{}
	flags.Var(&slackChannels, "slack-channel", "Slack channel id to collect while running")
	slackListIDs := watchDirFlags{}
	flags.Var(&slackListIDs, "slack-list", "Slack List id to collect while running")
	slackIncludeDMs := flags.Bool("slack-include-dms", false, "Opt into collecting Slack direct and group direct messages")

	if err := flags.Parse(args); err != nil {
		return workgraph.DaemonConfig{}, false
	}

	return workgraph.DaemonConfig{
		HomeDir:         *homeDir,
		DatabasePath:    *databasePath,
		WatchDirs:       watchDirs,
		SlackToken:      *slackToken,
		SlackChannels:   slackChannels,
		SlackListIDs:    slackListIDs,
		SlackIncludeDMs: *slackIncludeDMs,
		SlackAPIBaseURL: *slackAPIBaseURL,
	}, true
}

type watchDirFlags []string

func (flags *watchDirFlags) String() string {
	return fmt.Sprint([]string(*flags))
}

func (flags *watchDirFlags) Set(value string) error {
	*flags = append(*flags, value)
	return nil
}
