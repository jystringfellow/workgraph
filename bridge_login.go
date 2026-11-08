package workgraph

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

type BridgeLoginConfig struct {
	HomeDir       string
	Client        string
	ClientCommand string
	Stdin         io.Reader
	Stdout        io.Writer
	Stderr        io.Writer
}

func bridgeLoginArgs(client string) ([]string, error) {
	switch client {
	case "claude-code":
		return []string{"auth", "login"}, nil
	case "codex", "copilot":
		return []string{"login"}, nil
	}
	return nil, fmt.Errorf("%s has no verified sign-in command; sign in with %s directly", client, client)
}

func BridgeLogin(config BridgeLoginConfig) (string, error) {
	homeDir, err := connectorHomeDir(config.HomeDir)
	if err != nil {
		return "", err
	}
	client, err := normalizePluginClient(config.Client)
	if err != nil {
		return "", err
	}
	args, err := bridgeLoginArgs(client)
	if err != nil {
		return "", err
	}
	settings, err := bridgeWorkerSettingsFor(homeDir, client)
	if err != nil {
		return "", err
	}
	binding := signedInClientBinding{ConfigDir: settings.ConfigDir}
	if signOut, signedOut := readBridgeSignedOut(homeDir, client); signedOut {
		binding.ConfigDir = signOut.ConfigDir
	}
	if err := verifySignedInClientBinding(binding); err != nil {
		return "", fmt.Errorf("verify client config directory: %w", err)
	}
	command := exec.Command(pluginClientCommand(client, config.ClientCommand), args...)
	command.Env = applySignedInClientBinding(os.Environ(), client, binding)
	command.Stdin, command.Stdout, command.Stderr = config.Stdin, config.Stdout, config.Stderr
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("sign in to %s: %w", client, err)
	}
	clearBridgeSignedOutFor(homeDir, client, binding.ConfigDir)
	return fmt.Sprintf("Signed in %s bridge worker (config dir: %s).\nPending capture resumes on the next scheduled drain; run workgraph bridge drain --client %s to resume now.",
		client, signedInClientBindingLabel(binding), client), nil
}
