package workgraph

import (
	"strings"
	"sync"
	"testing"
)

func TestConnectorRuntimeFileReadersNeverSeeAPartialWrite(t *testing.T) {
	homeDir := t.TempDir()
	state := connectorRuntimeFile{Connectors: map[string]connectorRuntimeEntry{
		githubPRWatchConnectorID: {DeferredReason: strings.Repeat("GitHub timed out on the query (HTTP 502) ", 200)},
	}}
	if err := writeConnectorRuntimeFile(homeDir, state); err != nil {
		t.Fatalf("seed connector settings: %v", err)
	}

	done := make(chan struct{})
	var writer sync.WaitGroup
	writer.Add(1)
	go func() {
		defer writer.Done()
		for {
			select {
			case <-done:
				return
			default:
			}
			if err := writeConnectorRuntimeFile(homeDir, state); err != nil {
				t.Errorf("write connector settings: %v", err)
				return
			}
		}
	}()
	defer func() {
		close(done)
		writer.Wait()
	}()

	for range 2000 {
		read, err := readConnectorRuntimeFile(homeDir)
		if err != nil {
			t.Fatalf("expected readers never to observe a partial connector settings file: %v", err)
		}
		if read.entry(githubPRWatchConnectorID).DeferredReason != state.Connectors[githubPRWatchConnectorID].DeferredReason {
			t.Fatalf("expected readers to observe a complete write")
		}
	}
}
