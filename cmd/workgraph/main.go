package main

import (
	"fmt"
	"io"
	"os"

	workgraph "github.com/jystringfellow/workgraph"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	identity := currentBuildIdentity()
	workgraph.SetProcessBuildIdentity(workgraph.BuildIdentity{
		Version: identity.Version,
		Commit:  identity.Commit,
		Built:   identity.BuildDate,
	})
	if len(args) > 0 && args[0] == "help" {
		if len(args) == 2 && (args[1] == "-h" || args[1] == "--help") {
			return runHelp([]string{"help"}, false, stdout, stderr)
		}
		return runHelp(args[1:], false, stdout, stderr)
	}
	if len(args) == 1 && isHelpArgument(args[0]) {
		return runHelp(nil, false, stdout, stderr)
	}
	if len(args) > 1 && isHelpArgument(args[len(args)-1]) && !isAIRunPassthrough(args) {
		return runHelp(args[:len(args)-1], true, stdout, stderr)
	}

	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph <command>")
		return 2
	}

	switch args[0] {
	case "ai":
		return runAI(args[1:], os.Stdin, stdout, stderr)
	case "init":
		return runInit(args[1:], stdout, stderr)
	case "settings":
		return runSettings(args[1:], stdout, stderr)
	case "network":
		return runNetwork(args[1:], stdout, stderr)
	case "security":
		return runSecurity(args[1:], stdout, stderr)
	case "connectors":
		return runConnectors(args[1:], stdout, stderr)
	case "capture":
		return runCapture(args[1:], os.Stdin, stdout, stderr)
	case "bridge":
		return runBridge(args[1:], os.Stdin, stdout, stderr)
	case "plugin":
		return runPlugin(args[1:], stdout, stderr)
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
		return runLLM(args[1:], os.Stdin, stdout, stderr)
	case "events":
		return runEvents(args[1:], stdout, stderr)
	case "associations":
		return runAssociations(args[1:], stdout, stderr)
	case "suggestions":
		return runSuggestions(args[1:], stdout, stderr)
	case "review":
		return runReview(args[1:], stdout, stderr)
	case "notion":
		return runNotion(args[1:], stdout, stderr)
	case "memory":
		return runMemory(args[1:], stdout, stderr)
	case "projects":
		return runProjects(args[1:], stdout, stderr)
	case "start":
		return runCaptureStart(args[1:], stdout, stderr)
	case "service":
		return runService(args[1:], stdout, stderr)
	case "status":
		return runCaptureStatus(args[1:], stdout, stderr)
	case "stop":
		return runCaptureStop(args[1:], stdout, stderr)
	case "next":
		return runNext(args[1:], stdout, stderr)
	case "today":
		return runToday(args[1:], stdout, stderr)
	case "version":
		return runVersion(args[1:], stdout, stderr)
	case "resume":
		return runResume(args[1:], stdout, stderr)
	case "slack":
		return runSlack(args[1:], stdout, stderr)
	case "__capture-worker":
		return runCaptureWorker(args[1:], stderr)
	case "__capture-supervisor":
		return runCaptureSupervisor(args[1:], stderr)
	case "__ai-native-session":
		return runAINativeSessionHook(args[1:], os.Stdin, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", args[0])
		return 2
	}
}

type watchDirFlags []string

type repeatedStringFlags []string

func (flags *repeatedStringFlags) String() string {
	return fmt.Sprint([]string(*flags))
}

func (flags *repeatedStringFlags) Set(value string) error {
	*flags = append(*flags, value)
	return nil
}

func (flags *watchDirFlags) String() string {
	return fmt.Sprint([]string(*flags))
}

func (flags *watchDirFlags) Set(value string) error {
	*flags = append(*flags, value)
	return nil
}
