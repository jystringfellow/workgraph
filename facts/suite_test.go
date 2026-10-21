package facts

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var workgraphFactsBinary string

func TestMain(m *testing.M) {
	if dir := os.Getenv("WORKGRAPH_FACTS_FAKE_GH_DIR"); dir != "" {
		os.Exit(serveFakeGHMergeQuery(dir, os.Getenv("WORKGRAPH_FACTS_FAKE_GH_INPUT")))
	}
	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "find facts working directory: %v\n", err)
		os.Exit(1)
	}
	tempDir, err := os.MkdirTemp("", "workgraph-facts-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create facts binary directory: %v\n", err)
		os.Exit(1)
	}
	binaryName := "workgraph"
	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}
	workgraphFactsBinary = filepath.Join(tempDir, binaryName)
	build := exec.Command("go", "build", "-o", workgraphFactsBinary, "./cmd/workgraph")
	build.Dir = filepath.Dir(workingDir)
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build workgraph facts binary: %v\n%s", err, output)
		_ = os.RemoveAll(tempDir)
		os.Exit(1)
	}

	if err := isolateUserHome(tempDir); err != nil {
		fmt.Fprintf(os.Stderr, "isolate facts user home: %v\n", err)
		_ = os.RemoveAll(tempDir)
		os.Exit(1)
	}

	code := m.Run()
	_ = os.RemoveAll(tempDir)
	os.Exit(code)
}

func isolateUserHome(tempDir string) error {
	goEnvNames := []string{"GOPATH", "GOMODCACHE", "GOCACHE", "GOENV"}
	output, err := exec.Command("go", append([]string{"env"}, goEnvNames...)...).Output()
	if err != nil {
		return fmt.Errorf("read go env: %w", err)
	}
	values := strings.Split(strings.TrimSuffix(string(output), "\n"), "\n")
	if len(values) != len(goEnvNames) {
		return fmt.Errorf("unexpected go env output %q", output)
	}
	for index, name := range goEnvNames {
		if err := os.Setenv(name, values[index]); err != nil {
			return err
		}
	}
	userHome := filepath.Join(tempDir, "home")
	if err := os.MkdirAll(userHome, 0o700); err != nil {
		return err
	}
	if err := os.Setenv("HOME", userHome); err != nil {
		return err
	}
	return os.Setenv("USERPROFILE", userHome)
}
