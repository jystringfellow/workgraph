package workgraph

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestValidatedBridgeParamsRequireBoundedConnectorScope(t *testing.T) {
	tests := []struct {
		connector string
		valid     string
		canonical string
		invalid   string
	}{
		{"github", `{"repositories":["demo/repository"]}`, "", `{}`},
		{"slack", `{"channels":["C0DEMO123"],"include_dms":false}`, "", `{"channels":[]}`},
		{"slack.lists", `{"lists":["F0DEMO123"]}`, `{"done_column":"Done","lists":["F0DEMO123"],"row_key_candidates":[["Related Message"],["Title","Cycle"],["Title"]]}`, `{}`},
		{"mail.microsoft", `{"folders":["inbox"],"preview_limit":500}`, "", `{"folders":[]}`},
		{"calendar.microsoft", `{"calendars":["primary"],"past_days":7,"future_days":30}`, "", `{"calendars":["primary"],"past_days":7}`},
		{"azure.boards", `{"organization":"example-org","project":"Demo","area_path":"Demo"}`, "", `{"organization":"example-org"}`},
	}
	for _, test := range tests {
		t.Run(test.connector, func(t *testing.T) {
			params, err := validatedBridgeParams(test.connector, json.RawMessage(test.valid))
			if err != nil {
				t.Fatalf("validate bounded scope: %v", err)
			}
			var want, got any
			canonical := test.canonical
			if canonical == "" {
				canonical = test.valid
			}
			if err := json.Unmarshal([]byte(canonical), &want); err != nil {
				t.Fatalf("decode wanted scope: %v", err)
			}
			if err := json.Unmarshal(params, &got); err != nil {
				t.Fatalf("decode canonical scope: %v", err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("expected canonical scope %s, got %s", test.valid, params)
			}
			if _, err := validatedBridgeParams(test.connector, json.RawMessage(test.invalid)); err == nil {
				t.Fatalf("expected unbounded scope %s to fail", test.invalid)
			}
		})
	}
}

func TestValidatedBridgeParamsRejectMalformedObjectsAndSecrets(t *testing.T) {
	for _, raw := range []string{`{"channels":`, `[]`, `{"channels":["C0DEMO123"],"api_key":"secret"}`} {
		if _, err := validatedBridgeParams("slack", json.RawMessage(raw)); err == nil {
			t.Fatalf("expected bridge params %q to fail", raw)
		} else if raw == `[]` && !strings.Contains(err.Error(), "JSON object") {
			t.Fatalf("expected object validation error, got %v", err)
		}
	}
	if _, err := validatedBridgeParams("git", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected local git capture to reject bridged parameters")
	}
	if _, err := validatedBridgeParams("notion", json.RawMessage(`{"roots":["demo-root"],"preview_limit":500}`)); err == nil || !strings.Contains(err.Error(), "notion only supports direct capture") {
		t.Fatalf("expected Notion to reject bridged parameters, got %v", err)
	}
}

func TestValidatedBridgeParamsAllowExplicitParticipantStrategies(t *testing.T) {
	for connector, raw := range map[string]string{
		"slack":        `{"scope":"participant","identity":"@Me","include":["authored","mentions","thread_participation"]}`,
		"azure.boards": `{"organization":"example-org","scope":"participant","identity":"@Me","include":["authored","assigned"]}`,
	} {
		if _, err := validatedBridgeParams(connector, json.RawMessage(raw)); err != nil {
			t.Fatalf("validate %s participant scope: %v", connector, err)
		}
	}
	if _, err := validatedBridgeParams("azure.boards", json.RawMessage(`{"organization":"example-org","scope":"participant","identity":"@Me","include":["mentions"]}`)); err == nil {
		t.Fatal("expected unsupported Azure participant include to fail")
	}
}

func TestClaudeProviderToolsRequireExactExplicitNames(t *testing.T) {
	tools, err := validateClaudeProviderTools("claude-code", []string{"mcp__provider__read", "mcp__provider__read"})
	if err != nil || len(tools) != 1 {
		t.Fatalf("validate exact provider tool: tools=%v error=%v", tools, err)
	}
	for _, test := range []struct {
		client string
		tool   string
	}{
		{"claude-code", "mcp__provider__*"},
		{"claude-code", "Bash(workgraph *)"},
		{"claude-code", "mcp__plugin_workgraph_workgraph__capture_ingest"},
		{"codex", "mcp__provider__read"},
	} {
		if _, err := validateClaudeProviderTools(test.client, []string{test.tool}); err == nil {
			t.Fatalf("expected %s provider tool %q to fail", test.client, test.tool)
		}
	}
}
