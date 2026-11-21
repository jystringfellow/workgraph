package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	workgraph "github.com/jystringfellow/workgraph"
)

func runSecurity(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph security <report>")
		return 2
	}
	if args[0] != "report" {
		fmt.Fprintf(stderr, "unknown security command: %s\n", args[0])
		return 2
	}
	flags := flag.NewFlagSet("security report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	format := flags.String("format", "text", "output format: text or json")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph security report [--format text|json]")
		return 2
	}
	result, err := workgraph.SecurityReport(workgraph.SecurityReportConfig{HomeDir: *homeDir, Format: *format})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph security report: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runNetwork(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph network <destinations>")
		return 2
	}
	switch args[0] {
	case "destinations":
		return runNetworkDestinations(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown network command: %s\n", args[0])
		return 2
	}
}

func runNetworkDestinations(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("network destinations", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	format := flags.String("format", "text", "output format: text or json")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph network destinations [--format text|json]")
		return 2
	}

	result, err := workgraph.NetworkDestinations(workgraph.NetworkDestinationsConfig{
		HomeDir: *homeDir,
		Format:  *format,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph network destinations: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
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

func runInit(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("init", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	memoryDir := flags.String("memory", "", "workgraph memory directory")
	databasePath := flags.String("database", "", "workgraph SQLite database path")
	force := flags.Bool("force", false, "Refresh init-owned defaults such as settings.json")

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

func runSettings(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph settings <command>")
		return 2
	}

	switch args[0] {
	case "add-ignore-name":
		return runSettingsIgnoreName(args[1:], false, stdout, stderr)
	case "add-ignore-path":
		return runSettingsIgnorePath(args[1:], false, stdout, stderr)
	case "add-watch":
		return runSettingsAddWatch(args[1:], stdout, stderr)
	case "disable-agent-checkpoints":
		return runSettingsAgentCheckpoints(args[1:], false, stdout, stderr)
	case "enable-agent-checkpoints":
		return runSettingsAgentCheckpoints(args[1:], true, stdout, stderr)
	case "get":
		return runSettingsGet(args[1:], stdout, stderr)
	case "doctor":
		return runSettingsDoctor(args[1:], stdout, stderr)
	case "remove-ignore-name":
		return runSettingsIgnoreName(args[1:], true, stdout, stderr)
	case "remove-ignore-path":
		return runSettingsIgnorePath(args[1:], true, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown settings command: %s\n", args[0])
		return 2
	}
}

func runSettingsAgentCheckpoints(args []string, enabled bool, stdout io.Writer, stderr io.Writer) int {
	command := "disable-agent-checkpoints"
	if enabled {
		command = "enable-agent-checkpoints"
	}
	flags := flag.NewFlagSet("settings "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph settings "+command)
		return 2
	}

	result, err := workgraph.SetAIAgentCheckpoints(workgraph.SettingsAgentCheckpointsConfig{
		HomeDir: *homeDir,
		Enabled: enabled,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph settings %s: %v\n", command, err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSettingsGet(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("settings get", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	format := flags.String("format", "text", "output format: text or json")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph settings get [--format text|json]")
		return 2
	}

	result, err := workgraph.GetSettings(workgraph.SettingsGetConfig{
		HomeDir: *homeDir,
		Format:  *format,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph settings get: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSettingsDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("settings doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph settings doctor")
		return 2
	}

	result, err := workgraph.DoctorSettings(workgraph.SettingsDoctorConfig{
		HomeDir: *homeDir,
	})
	fmt.Fprintln(stdout, result.Message)
	if err != nil {
		return 1
	}
	return 0
}

func runSettingsAddWatch(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("settings add-watch", flag.ContinueOnError)
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
		fmt.Fprintln(stderr, "usage: workgraph settings add-watch [path]")
		return 2
	}

	result, err := workgraph.AddWatchDir(workgraph.SettingsWatchConfig{
		HomeDir: *homeDir,
		Path:    path,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph settings add-watch: %v\n", err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSettingsIgnorePath(args []string, remove bool, stdout io.Writer, stderr io.Writer) int {
	command := "add-ignore-path"
	if remove {
		command = "remove-ignore-path"
	}
	flags := flag.NewFlagSet("settings "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintf(stderr, "usage: workgraph settings %s <path>\n", command)
		return 2
	}

	config := workgraph.SettingsIgnoreConfig{HomeDir: *homeDir, Path: flags.Arg(0)}
	var result workgraph.SettingsIgnoreResult
	var err error
	if remove {
		result, err = workgraph.RemoveIgnorePath(config)
	} else {
		result, err = workgraph.AddIgnorePath(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph settings %s: %v\n", command, err)
		return 1
	}

	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runSettingsIgnoreName(args []string, remove bool, stdout io.Writer, stderr io.Writer) int {
	command := "add-ignore-name"
	if remove {
		command = "remove-ignore-name"
	}
	flags := flag.NewFlagSet("settings "+command, flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 1 {
		fmt.Fprintf(stderr, "usage: workgraph settings %s <name>\n", command)
		return 2
	}

	config := workgraph.SettingsIgnoreConfig{HomeDir: *homeDir, Name: flags.Arg(0)}
	var result workgraph.SettingsIgnoreResult
	var err error
	if remove {
		result, err = workgraph.RemoveIgnoreName(config)
	} else {
		result, err = workgraph.AddIgnoreName(config)
	}
	if err != nil {
		fmt.Fprintf(stderr, "workgraph settings %s: %v\n", command, err)
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
			Progress:        stdout,
		})
		var stillStarting *workgraph.DaemonStillStartingError
		if errors.As(err, &stillStarting) {
			fmt.Fprintln(stdout, stillStarting.Error())
			return workgraph.ExitDaemonStillStarting
		}
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

func runCaptureSupervisor(args []string, stderr io.Writer) int {
	executable, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "workgraph capture supervisor: find executable: %v\n", err)
		return 1
	}
	command := exec.Command(executable, append([]string{"__capture-worker"}, args...)...)
	command.Stdin = os.Stdin
	command.Stdout = stderr
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			if status, ok := exitError.ProcessState.Sys().(syscall.WaitStatus); ok && status.Signaled() && status.Signal() == syscall.SIGTERM {
				return 0
			}
		}
		fmt.Fprintf(stderr, "workgraph capture supervisor: %v\n", err)
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
