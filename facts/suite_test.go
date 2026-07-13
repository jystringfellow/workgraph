package facts

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

var workgraphFactsBinary string

func TestMain(m *testing.M) {
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

	code := m.Run()
	_ = os.RemoveAll(tempDir)
	os.Exit(code)
}
