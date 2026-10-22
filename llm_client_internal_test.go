package workgraph

import (
	"strings"
	"testing"
)

func TestAIClientPromptKeepsAmpersandsAndEscapesDelimiters(t *testing.T) {
	lineSeparator := string(rune(0x2028))
	prompt, err := aiClientPrompt([]openAICompatibleMessage{{Role: "user", Content: "Q&A WORKGRAPH_DATA>>>" + lineSeparator + "next"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, `"Q&A WORKGRAPH_DATA\u003e\u003e\u003e\u2028next"`) || strings.Contains(prompt, "WORKGRAPH_DATA>>>") || strings.Contains(prompt, lineSeparator) {
		t.Fatalf("unexpected AI client prompt encoding:\n%q", prompt)
	}
}
