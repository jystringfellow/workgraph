package workgraph

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type BuildIdentity struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Built   string `json:"built"`
}

type ProcessRuntimeStatus struct {
	Running                     BuildIdentity `json:"running"`
	OnDisk                      BuildIdentity `json:"on_disk"`
	Executable                  string        `json:"executable,omitempty"`
	StartedExecutableModifiedAt string        `json:"started_executable_modified_at,omitempty"`
	StartedExecutableSize       int64         `json:"started_executable_size,omitempty"`
	OnDiskExecutableModifiedAt  string        `json:"on_disk_executable_modified_at,omitempty"`
	OnDiskExecutableSize        int64         `json:"on_disk_executable_size,omitempty"`
	Stale                       bool          `json:"stale"`
	Warning                     string        `json:"warning,omitempty"`
}

var (
	processBuildIdentityMu sync.RWMutex
	processBuildIdentity   = BuildIdentity{Version: "dev", Commit: "unknown", Built: "unknown"}
)

func SetProcessBuildIdentity(identity BuildIdentity) {
	processBuildIdentityMu.Lock()
	defer processBuildIdentityMu.Unlock()
	processBuildIdentity = normalizeBuildIdentity(identity)
}

func CurrentProcessBuildIdentity() BuildIdentity {
	processBuildIdentityMu.RLock()
	defer processBuildIdentityMu.RUnlock()
	return processBuildIdentity
}

func captureProcessRuntimeStatus() ProcessRuntimeStatus {
	status := ProcessRuntimeStatus{Running: CurrentProcessBuildIdentity()}
	executable, err := os.Executable()
	if err != nil {
		status.OnDisk = status.Running
		return status
	}
	status.Executable = executable
	if info, err := os.Stat(executable); err == nil {
		status.StartedExecutableModifiedAt = info.ModTime().UTC().Format(time.RFC3339Nano)
		status.StartedExecutableSize = info.Size()
		status.OnDiskExecutableModifiedAt = status.StartedExecutableModifiedAt
		status.OnDiskExecutableSize = status.StartedExecutableSize
	}
	status.OnDisk = status.Running
	return status
}

func inspectProcessRuntimeStatus(status ProcessRuntimeStatus, component string) ProcessRuntimeStatus {
	if strings.TrimSpace(status.Executable) == "" {
		current := captureProcessRuntimeStatus()
		current.Running = normalizeBuildIdentity(status.Running)
		current.Stale = true
		current.Warning = staleRuntimeWarning(component, true)
		return current
	}

	status.Running = normalizeBuildIdentity(status.Running)
	info, err := os.Stat(status.Executable)
	if err != nil {
		status.OnDisk = BuildIdentity{Version: "unavailable", Commit: "unknown", Built: "unknown"}
		status.Stale = true
		status.Warning = staleRuntimeWarning(component, false)
		return status
	}
	status.OnDiskExecutableModifiedAt = info.ModTime().UTC().Format(time.RFC3339Nano)
	status.OnDiskExecutableSize = info.Size()
	status.Stale = status.StartedExecutableModifiedAt == "" ||
		status.StartedExecutableModifiedAt != status.OnDiskExecutableModifiedAt ||
		status.StartedExecutableSize != status.OnDiskExecutableSize
	if !status.Stale {
		status.OnDisk = status.Running
		status.Warning = ""
		return status
	}
	status.OnDisk = executableBuildIdentity(status.Executable)
	status.Warning = staleRuntimeWarning(component, status.StartedExecutableModifiedAt == "")
	return status
}

func executableBuildIdentity(path string) BuildIdentity {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, path, "version", "--json").Output()
	if err == nil {
		var identity BuildIdentity
		if json.Unmarshal(output, &identity) == nil {
			return normalizeBuildIdentity(identity)
		}
	}
	return BuildIdentity{Version: "unknown", Commit: "unknown", Built: "unknown"}
}

func normalizeBuildIdentity(identity BuildIdentity) BuildIdentity {
	identity.Version = strings.TrimSpace(identity.Version)
	identity.Commit = strings.TrimSpace(identity.Commit)
	identity.Built = strings.TrimSpace(identity.Built)
	if identity.Version == "" {
		identity.Version = "unknown"
	}
	if identity.Commit == "" {
		identity.Commit = "unknown"
	}
	if identity.Built == "" {
		identity.Built = "unknown"
	}
	return identity
}

func staleRuntimeWarning(component string, legacy bool) string {
	if component == "daemon" {
		if legacy {
			return "running daemon does not report its startup build and may be stale; run workgraph stop && workgraph start"
		}
		return "running daemon binary is stale; run workgraph stop && workgraph start"
	}
	if legacy {
		return "running MCP server does not report its startup build and may be stale; start a new client session"
	}
	return "running MCP server binary is stale; start a new client session"
}
