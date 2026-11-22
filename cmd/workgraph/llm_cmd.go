package main

import (
	stdflag "flag"
	"fmt"
	"io"
	"strings"

	workgraph "github.com/jystringfellow/workgraph"
)

func runLLM(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm <command>")
		return 2
	}

	switch args[0] {
	case "add":
		return runLLMAdd(args[1:], stdout, stderr)
	case "connect":
		return runLLMConnect(args[1:], stdout, stderr)
	case "list":
		return runLLMList(args[1:], stdout, stderr)
	case "remove":
		return runLLMRemove(args[1:], stdout, stderr)
	case "use":
		return runLLMUse(args[1:], stdout, stderr)
	case "set":
		return runLLMSet(args[1:], stdout, stderr)
	case "hosted":
		return runLLMHosted(args[1:], stdout, stderr)
	case "test":
		return runLLMTest(args[1:], stdout, stderr)
	case "doctor":
		return runLLMDoctor(args[1:], stdout, stderr)
	case "summarize":
		return runLLMSummarize(args[1:], stdout, stderr)
	case "associate":
		return runLLMAssociate(args[1:], stdin, stdout, stderr)
	case "sources":
		return runLLMSources(args[1:], stdout, stderr)
	case "verify-backlog":
		return runLLMVerifyBacklog(args[1:], stdout, stderr)
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
	client := flags.String("client", "", "Signed-in AI client: codex, claude-code, copilot, or agy")
	baseURL := flags.String("base-url", "", "OpenAI-compatible or system-one base URL")
	model := flags.String("model", "", "LLM model")
	apiKeyEnv := flags.String("api-key-env", "", "Environment variable containing the API key")
	awsProfile := flags.String("aws-profile", "", "AWS profile for Bedrock")
	region := flags.String("region", "", "Cloud provider region")
	modelARN := flags.String("model-arn", "", "Bedrock model or inference profile ARN")
	configDir := flags.String("config-dir", "", "signed-in AI client configuration directory")
	execution := flags.String("execution", "", "ai-client execution: bridge runs scheduled tasks in the bridge worker")
	autoApply := flags.Float64("auto-apply-confidence", 0, "Lowest confidence from this profile that links a unit automatically, 0.5 to 1.0; default 0.95")
	verifyFloor := flags.Float64("verify-floor", 0, "Lowest confidence from this profile that is sent to the associate-verify profile, 0 to 1; default 0.6")

	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	autoApplySetting, verifyFloorSetting := thresholdFlags(flags, autoApply, verifyFloor)

	result, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{
		HomeDir:    *homeDir,
		Name:       profile,
		Provider:   *provider,
		Client:     *client,
		BaseURL:    *baseURL,
		Model:      *model,
		APIKeyEnv:  *apiKeyEnv,
		AWSProfile: *awsProfile,
		Region:     *region,
		ModelARN:   *modelARN,
		ConfigDir:  *configDir,
		Execution:  *execution,

		AutoApplyConfidence: autoApplySetting,
		VerifyFloor:         verifyFloorSetting,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm add: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func thresholdFlags(flags *stdflag.FlagSet, autoApply, verifyFloor *float64) (*float64, *float64) {
	var autoApplySetting, verifyFloorSetting *float64
	flags.Visit(func(set *stdflag.Flag) {
		switch set.Name {
		case "auto-apply-confidence":
			autoApplySetting = autoApply
		case "verify-floor":
			verifyFloorSetting = verifyFloor
		}
	})
	return autoApplySetting, verifyFloorSetting
}

func runLLMSet(args []string, stdout io.Writer, stderr io.Writer) int {
	usage := "usage: workgraph llm set <profile> [--auto-apply-confidence <0.5-1.0>] [--verify-floor <0-1>]"
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	profile := args[0]
	flags := flag.NewFlagSet("llm set "+profile, flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	autoApply := flags.Float64("auto-apply-confidence", 0, "Lowest confidence from this profile that links a unit automatically, 0.5 to 1.0")
	verifyFloor := flags.Float64("verify-floor", 0, "Lowest confidence from this profile that is sent to the associate-verify profile, 0 to 1")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, usage)
		return 2
	}
	autoApplySetting, verifyFloorSetting := thresholdFlags(flags, autoApply, verifyFloor)
	result, err := workgraph.SetLLMProfile(workgraph.LLMSetProfileConfig{HomeDir: *homeDir, Name: profile, AutoApplyConfidence: autoApplySetting, VerifyFloor: verifyFloorSetting})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm set: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMConnect(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm connect <codex|claude-code|copilot|agy> [options]")
		return 2
	}
	client := args[0]
	flags := flag.NewFlagSet("llm connect "+client, flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	name := flags.String("name", "", "Profile name; defaults to the client id")
	task := flags.String("for", "", "Task to route to this profile")
	model := flags.String("model", "", "Optional client model; defaults to the client's configured model")
	configDir := flags.String("config-dir", "", "signed-in AI client configuration directory")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm connect <codex|claude-code|copilot|agy> [options]")
		return 2
	}
	result, err := workgraph.ConnectLLMClient(workgraph.LLMConnectClientConfig{
		HomeDir:   *homeDir,
		Client:    client,
		Name:      *name,
		Task:      *task,
		Model:     *model,
		ConfigDir: *configDir,
	})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm connect: %v\n", err)
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

func runLLMHosted(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm hosted <status|enable|disable>")
		return 2
	}
	switch args[0] {
	case "status":
		return runLLMHostedStatus(args[1:], stdout, stderr)
	case "enable":
		return runLLMHostedEnable(args[1:], stdout, stderr)
	case "disable":
		return runLLMHostedDisable(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown llm hosted command: %s\n", args[0])
		return 2
	}
}

func runLLMHostedStatus(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm hosted status", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm hosted status")
		return 2
	}
	result, err := workgraph.HostedLLMStatus(workgraph.LLMHostedConfig{HomeDir: *homeDir})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm hosted status: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMHostedEnable(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm hosted enable", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm hosted enable")
		return 2
	}
	result, err := workgraph.EnableHostedLLM(workgraph.LLMHostedConfig{HomeDir: *homeDir})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm hosted enable: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, result.Message)
	return 0
}

func runLLMHostedDisable(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm hosted disable", flag.ContinueOnError)
	flags.SetOutput(stderr)
	homeDir := flags.String("home", "", "workgraph home directory")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm hosted disable")
		return 2
	}
	result, err := workgraph.DisableHostedLLM(workgraph.LLMHostedConfig{HomeDir: *homeDir})
	if err != nil {
		fmt.Fprintf(stderr, "workgraph llm hosted disable: %v\n", err)
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

func runLLMDoctor(args []string, stdout io.Writer, stderr io.Writer) int {
	flags := flag.NewFlagSet("llm doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)

	homeDir := flags.String("home", "", "workgraph home directory")
	profile := flags.String("profile", "", "LLM profile to inspect")

	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: workgraph llm doctor [--profile name]")
		return 2
	}

	result, err := workgraph.DoctorLLMProfiles(workgraph.LLMDoctorConfig{
		HomeDir: *homeDir,
		Profile: *profile,
	})
	fmt.Fprintln(stdout, result.Message)
	if err != nil {
		return 1
	}
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
		HomeDir:   *homeDir,
		GitEmails: workgraph.LocalGitEmails(workgraph.LocalGitIdentityConfig{HomeDir: *homeDir}),
		DryRun:    *dryRun,
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
