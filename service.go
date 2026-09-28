package workgraph

import (
	"crypto/sha256"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type captureService struct {
	home     string
	path     string
	label    string
	domain   string
	platform string
}

func installedCaptureService(home string) (*captureService, error) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return nil, nil
	}
	s, err := resolveCaptureService(home)
	if err != nil {
		return nil, err
	}
	installed, err := s.installed()
	if err != nil || !installed {
		return nil, err
	}
	return &s, nil
}

func resolveCaptureService(home string) (captureService, error) {
	home, err := resolveHomeDir(home)
	if err != nil {
		return captureService{}, err
	}
	home, err = filepath.Abs(home)
	if err != nil {
		return captureService{}, err
	}
	userHome, err := os.UserHomeDir()
	if err != nil {
		return captureService{}, err
	}
	sum := sha256.Sum256([]byte(home))
	id := fmt.Sprintf("%x", sum[:8])
	s := captureService{home: home, platform: runtime.GOOS, domain: "gui/" + strconv.Itoa(os.Getuid())}
	switch runtime.GOOS {
	case "darwin":
		s.label = "com.workgraph.capture." + id
		s.path = filepath.Join(userHome, "Library", "LaunchAgents", s.label+".plist")
	case "linux":
		configDir, err := os.UserConfigDir()
		if err != nil {
			return captureService{}, err
		}
		s.label = "workgraph-capture-" + id + ".service"
		s.path = filepath.Join(configDir, "systemd", "user", s.label)
	default:
		return captureService{}, fmt.Errorf("capture services require macOS or Linux")
	}
	return s, nil
}

func (s captureService) installed() (bool, error) {
	_, err := os.Stat(s.path)
	if os.IsNotExist(err) {
		return false, nil
	}
	return err == nil, err
}

func serviceCommand(name string, args ...string) (string, error) {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func (s captureService) stop() error {
	if s.platform == "linux" {
		_, err := serviceCommand("systemctl", "--user", "disable", "--now", s.label)
		return err
	}
	if _, err := serviceCommand("launchctl", "disable", s.domain+"/"+s.label); err != nil {
		return err
	}
	output, err := serviceCommand("launchctl", "bootout", s.domain+"/"+s.label)
	if err != nil && !strings.Contains(strings.ToLower(output), "could not find service") && !strings.Contains(strings.ToLower(output), "no such process") {
		return err
	}
	return nil
}

func (s captureService) start() error {
	if s.platform == "linux" {
		if _, err := serviceCommand("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		_, err := serviceCommand("systemctl", "--user", "enable", "--now", s.label)
		return err
	}
	if _, err := serviceCommand("launchctl", "enable", s.domain+"/"+s.label); err != nil {
		return err
	}
	if _, err := serviceCommand("launchctl", "print", s.domain+"/"+s.label); err == nil {
		_, err = serviceCommand("launchctl", "kickstart", s.domain+"/"+s.label)
		return err
	}
	_, err := serviceCommand("launchctl", "bootstrap", s.domain, s.path)
	return err
}

func CaptureService(action string, home string) (string, error) {
	s, err := resolveCaptureService(home)
	if err != nil {
		return "", err
	}
	installed, err := s.installed()
	if err != nil {
		return "", err
	}
	switch action {
	case "install":
		if err := requireMemoryInitHome(s.home); err != nil {
			return "", err
		}
		if !installed {
			status, err := DaemonStatusForHome(s.home)
			if err != nil {
				return "", err
			}
			if status.Running {
				return "", fmt.Errorf("capture is already running; run workgraph stop --home %q before installing a service", s.home)
			}
		}
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		userHome, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		definition := captureServiceDefinition(s, executable, userHome)
		logFile, err := os.OpenFile(daemonLogPath(s.home), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return "", err
		}
		if err := logFile.Close(); err != nil {
			return "", err
		}
		if err := ensureUserOnlyFile(daemonLogPath(s.home)); err != nil {
			return "", err
		}
		if installed {
			if err := s.stop(); err != nil {
				return "", err
			}
		}
		if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
			return "", err
		}
		if err := os.WriteFile(s.path, []byte(definition), 0o600); err != nil {
			return "", err
		}
		if err := s.start(); err != nil {
			return "", fmt.Errorf("definition saved at %s, but activation failed: %w", s.path, err)
		}
		return "Capture service installed: " + s.path + "\nLogin startup and automatic restart enabled. Inspect capture with workgraph status.", nil
	case "status":
		if !installed {
			return "Capture service is not installed.", nil
		}
		var output string
		if s.platform == "darwin" {
			output, err = serviceCommand("launchctl", "print", s.domain+"/"+s.label)
		} else {
			output, err = serviceCommand("systemctl", "--user", "show", s.label, "--property=LoadState,ActiveState,SubState,UnitFileState,Result")
		}
		message := "Capture service definition: " + s.path + "\n" + strings.TrimSpace(output)
		if err != nil {
			message += "\nSupervisor unavailable or service not loaded: " + err.Error()
		}
		status, statusErr := DaemonStatusForHome(s.home)
		if statusErr != nil {
			return message, statusErr
		}
		return message + "\n" + status.Message, nil
	case "uninstall":
		if !installed {
			return "Capture service is not installed.", nil
		}
		if err := s.stop(); err != nil {
			return "", err
		}
		if err := os.Remove(s.path); err != nil {
			return "", err
		}
		if s.platform == "linux" {
			if _, err := serviceCommand("systemctl", "--user", "daemon-reload"); err != nil {
				return "", err
			}
		}
		return "Capture service removed: " + s.path + "\nEvents, memory, settings and logs were preserved.", nil
	default:
		return "", fmt.Errorf("unknown service command %q", action)
	}
}

func captureServiceDefinition(s captureService, executable, userHome string) string {
	args := []string{executable, "__capture-worker", "--home", s.home, "--database", filepath.Join(s.home, "workgraph.db")}
	environment := bridgeLaunchEnvironment(userHome, executable, executable)
	if s.platform == "darwin" {
		var arguments strings.Builder
		for _, arg := range args {
			arguments.WriteString("<string>" + html.EscapeString(arg) + "</string>\n")
		}
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array>%s</array>
<key>EnvironmentVariables</key><dict><key>HOME</key><string>%s</string><key>PATH</key><string>%s</string></dict>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><true/>
<key>ThrottleInterval</key><integer>10</integer>
<key>StandardOutPath</key><string>%s</string>
<key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, html.EscapeString(s.label), arguments.String(), html.EscapeString(userHome), html.EscapeString(environment["PATH"]), html.EscapeString(daemonLogPath(s.home)), html.EscapeString(daemonLogPath(s.home)))
	}
	for i := range args {
		args[i] = systemdArgument(args[i])
	}
	return fmt.Sprintf("[Unit]\nDescription=workgraph capture\nStartLimitIntervalSec=0\n\n[Service]\nType=simple\nExecStart=%s\nEnvironment=%s %s\nRestart=on-failure\nRestartSec=10\nUMask=0077\n\n[Install]\nWantedBy=default.target\n", strings.Join(args, " "), systemdValue("HOME="+userHome), systemdValue("PATH="+environment["PATH"]))
}

func systemdArgument(value string) string {
	return systemdValue(strings.ReplaceAll(value, "$", "$$"))
}

func systemdValue(value string) string {
	value = strings.NewReplacer("%", "%%", "\\", "\\\\", "\"", "\\\"", "\n", "\\n", "\r", "\\r", "\t", "\\t").Replace(value)
	return "\"" + value + "\""
}
