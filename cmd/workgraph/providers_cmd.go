package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	workgraph "github.com/jystringfellow/workgraph"
)

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
	case "connect-token":
		return runNotionConnectToken(args[1:], stdout, stderr)
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

func runNotionConnectToken(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("notion connect-token", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	token := flags.String("token", "", "Notion internal integration token")
	notionAPIBaseURL := flags.String("notion-api-base", "", "Notion API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph notion connect-token --token <token>")
		return 2
	}

	result, err := workgraph.ConnectNotionWithToken(workgraph.NotionConnectTokenConfig{
		HomeDir:    *homeDir,
		Token:      *token,
		APIBaseURL: *notionAPIBaseURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph notion connect-token: %v\n", err)
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
	optionsJSON := flags.String("options-json", "", "non-secret interpretation options for this Slack List")
	slackAPIBaseURL := flags.String("slack-api-base", "", "Slack API base URL")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	var options workgraph.SlackListOptions
	if strings.TrimSpace(*optionsJSON) != "" {
		decoder := json.NewDecoder(strings.NewReader(*optionsJSON))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&options); err != nil {
			fmt.Fprintf(stderr, "workgraph slack lists capture: options-json must be a Slack List options object: %v\n", err)
			return 1
		}
	}
	result, err := workgraph.CaptureSlackList(workgraph.SlackListCaptureConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		Token:        *token,
		ListID:       *listID,
		Options:      options,
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
	listOptionsJSON := flags.String("list-options-json", "", "non-secret per-List interpretation options keyed by List id")
	includeDMs := flags.Bool("include-dms", false, "Opt into collecting Slack direct and group direct messages")

	if err := flags.Parse(args); err != nil {
		return 2
	}

	var listOptions map[string]workgraph.SlackListOptions
	if strings.TrimSpace(*listOptionsJSON) != "" {
		decoder := json.NewDecoder(strings.NewReader(*listOptionsJSON))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&listOptions); err != nil {
			fmt.Fprintf(stderr, "workgraph slack connect: list-options-json must be an object keyed by List id: %v\n", err)
			return 1
		}
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
		ListOptions:      listOptions,
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
	case "prs":
		return runGitHubPRs(args[1:], stdout, stderr)
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
	paramsJSON := flags.String("params-json", "", "non-secret GitHub capture scope as a JSON object; defaults to participant scope for @me")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph github connect [--params-json '<scope-json>']")
		return 2
	}

	result, err := workgraph.ConnectGitHub(workgraph.ConnectorConnectConfig{
		HomeDir:       *homeDir,
		GitHubCommand: *gh,
		ParamsJSON:    json.RawMessage(*paramsJSON),
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

func runGitHubPRs(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph github prs <command>")
		return 2
	}

	switch args[0] {
	case "connect":
		return runGitHubPRsConnect(args[1:], stdout, stderr)
	case "capture":
		return runGitHubPRsCapture(args[1:], stdout, stderr)
	case "disconnect":
		return runGitHubPRsDisconnect(args[1:], stdout, stderr)
	case "hint":
		return runGitHubPRsHint(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown github prs command: %s\n", args[0])
		return 2
	}
}

func runGitHubPRsConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("github prs connect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	gh := flags.String("gh", "", "gh-compatible executable for GitHub access")
	paramsJSON := flags.String("params-json", "", "non-secret pull request watch scope as a JSON object; defaults to open_prs for @me")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph github prs connect [--params-json '<scope-json>']")
		return 2
	}

	result, err := workgraph.ConnectGitHubPRWatch(workgraph.ConnectorConnectConfig{
		HomeDir:       *homeDir,
		GitHubCommand: *gh,
		ParamsJSON:    json.RawMessage(*paramsJSON),
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph github prs connect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runGitHubPRsCapture(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("github prs capture", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	gh := flags.String("gh", "", "gh-compatible executable for GitHub access")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph github prs capture")
		return 2
	}

	result, err := workgraph.CaptureGitHubPRWatch(workgraph.GitHubPRWatchCaptureConfig{
		HomeDir:       *homeDir,
		DatabasePath:  *databasePath,
		GitHubCommand: *gh,
	})
	if result.Message != "" {
		fmt.Fprintln(stdout, result.Message)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph github prs capture: %v\n", err)
		return 1
	}
	return 0
}

func runGitHubPRsDisconnect(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("github prs disconnect", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph github prs disconnect")
		return 2
	}

	result, err := workgraph.DisconnectGitHubPRWatch(workgraph.ConnectorConnectConfig{HomeDir: *homeDir})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph github prs disconnect: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runGitHubPRsHint(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("github prs hint", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	prURL := flags.String("pr", "", "pull request URL whose details the next poll refreshes")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph github prs hint [--pr <url>]")
		return 2
	}

	result, err := workgraph.RecordGitHubPRWatchHint(workgraph.GitHubPRWatchHintConfig{
		HomeDir:      *homeDir,
		DatabasePath: *databasePath,
		PRURL:        *prURL,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph github prs hint: %v\n", err)
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
