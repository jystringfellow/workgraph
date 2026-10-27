package facts

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeGHGraphQL struct {
	Path     string
	dir      string
	queueDir string
	queued   int
}

func writeFakeGHGraphQL(t *testing.T, dir string) *fakeGHGraphQL {
	t.Helper()

	fake := &fakeGHGraphQL{
		Path:     filepath.Join(dir, "gh"),
		dir:      dir,
		queueDir: filepath.Join(dir, "gh-graphql-queue"),
	}
	testBinary, err := filepath.Abs(os.Args[0])
	if err != nil {
		t.Fatalf("resolve facts test binary: %v", err)
	}
	for _, sub := range []string{fake.queueDir, filepath.Join(dir, "gh-graphql-inputs"), filepath.Join(dir, "gh-merge")} {
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatalf("create fake gh dir: %v", err)
		}
	}
	script := `#!/bin/sh
dir="` + dir + `"
echo "$@" >> "$dir/gh.log"
if [ "$1" = "auth" ] && [ "$2" = "status" ]; then
  printf 'github.com\n  Logged in\n'
  exit 0
fi
if [ "$1" = "auth" ] && [ "$2" = "token" ]; then
  printf 'FAKE-GH-TOKEN-SENTINEL'
  exit 0
fi
if [ "$1" = "api" ] && [ "$2" = "graphql" ]; then
  input=""
  prev=""
  for arg in "$@"; do
    if [ "$prev" = "--input" ]; then input="$arg"; fi
    prev="$arg"
  done
  seq=$(cat "$dir/gh-input-count" 2>/dev/null || echo 0)
  seq=$((seq + 1))
  echo "$seq" > "$dir/gh-input-count"
  if [ -n "$input" ]; then cp "$input" "$dir/gh-graphql-inputs/$seq.json"; fi
  if [ -f "$dir/gh-detail-auto" ] && grep -q 'reviewThreads' "$input"; then
    WORKGRAPH_FACTS_FAKE_GH_DIR="$dir" WORKGRAPH_FACTS_FAKE_GH_INPUT="$input" exec "` + testBinary + `"
  fi
  if grep -q 'search(' "$input"; then
    if grep -q '"q":"is:pr is:open author:' "$input" && grep -Eq 'reviewDecision|viewerLatestReview|reviewRequests' "$input"; then
      printf 'gh: HTTP 502: 502 Bad Gateway (https://api.github.com/graphql)\n' >&2
      exit 1
    fi
    first=$(grep -o '"first":[0-9]*' "$input" | head -1 | cut -d: -f2)
    if [ -f "$dir/gh-search-page-limit" ] && [ "$first" -gt "$(cat "$dir/gh-search-page-limit")" ]; then
      printf 'gh: HTTP 502: 502 Bad Gateway (https://api.github.com/graphql)\n' >&2
      exit 1
    fi
  fi
  if grep -q 'mergeStateStatus' "$input"; then
    if grep -q 'search(' "$input"; then
      printf 'gh: HTTP 502: 502 Bad Gateway (https://api.github.com/graphql)\n' >&2
      exit 1
    fi
    WORKGRAPH_FACTS_FAKE_GH_DIR="$dir" WORKGRAPH_FACTS_FAKE_GH_INPUT="$input" exec "` + testBinary + `"
  fi
  n=$(cat "$dir/gh-graphql-count" 2>/dev/null || echo 0)
  n=$((n + 1))
  echo "$n" > "$dir/gh-graphql-count"
  response="$dir/gh-graphql-queue/$n"
  if [ ! -f "$response.out" ]; then
    printf 'fake gh: no queued graphql response %s\n' "$n" >&2
    exit 1
  fi
  if [ -f "$response.hook" ]; then sh "$response.hook"; fi
  cat "$response.out"
  if [ -f "$response.err" ]; then cat "$response.err" >&2; fi
  code=$(cat "$response.code" 2>/dev/null || echo 0)
  exit "$code"
fi
printf 'unexpected gh args: %s\n' "$*" >&2
exit 1
`
	if err := os.WriteFile(fake.Path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gh.log"), nil, 0o644); err != nil {
		t.Fatalf("write gh log: %v", err)
	}
	return fake
}

func (fake *fakeGHGraphQL) Queue(t *testing.T, stdout string) {
	t.Helper()
	fake.QueueResult(t, stdout, "", 0)
}

func (fake *fakeGHGraphQL) QueueResult(t *testing.T, stdout, stderr string, exitCode int) {
	t.Helper()
	stdout = fake.registerMergeStates(t, stdout)
	fake.queued++
	base := filepath.Join(fake.queueDir, fmt.Sprint(fake.queued))
	files := map[string]string{".out": stdout, ".code": fmt.Sprint(exitCode)}
	if stderr != "" {
		files[".err"] = stderr
	}
	for suffix, content := range files {
		if err := os.WriteFile(base+suffix, []byte(content), 0o644); err != nil {
			t.Fatalf("queue fake gh response: %v", err)
		}
	}
}

func (fake *fakeGHGraphQL) HookLastQueued(t *testing.T, script string) {
	t.Helper()
	base := filepath.Join(fake.queueDir, fmt.Sprint(fake.queued))
	if err := os.WriteFile(base+".hook", []byte(script), 0o644); err != nil {
		t.Fatalf("write fake gh hook: %v", err)
	}
}

func (fake *fakeGHGraphQL) Invocations(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fake.dir, "gh.log"))
	if err != nil {
		t.Fatalf("read gh log: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func (fake *fakeGHGraphQL) GraphQLInputs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(fake.dir, "gh-graphql-inputs"))
	if err != nil {
		t.Fatalf("read graphql inputs: %v", err)
	}
	inputs := make([]string, 0, len(entries))
	for index := 1; index <= len(entries); index++ {
		data, err := os.ReadFile(filepath.Join(fake.dir, "gh-graphql-inputs", fmt.Sprintf("%d.json", index)))
		if err != nil {
			t.Fatalf("read graphql input %d: %v", index, err)
		}
		inputs = append(inputs, string(data))
	}
	return inputs
}

func (fake *fakeGHGraphQL) RegisterMergeStates(t *testing.T, statusResponse string) {
	t.Helper()
	fake.registerMergeStates(t, statusResponse)
}

func (fake *fakeGHGraphQL) DelayTimeouts(t *testing.T, delay time.Duration) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fake.dir, "gh-timeout-delay"), []byte(delay.String()), 0o644); err != nil {
		t.Fatalf("write timeout delay: %v", err)
	}
}

func (fake *fakeGHGraphQL) AnswerDetailsEmpty(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fake.dir, "gh-detail-auto"), nil, 0o644); err != nil {
		t.Fatalf("enable automatic detail answers: %v", err)
	}
}

func (fake *fakeGHGraphQL) LimitSearchPageSize(t *testing.T, limit int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fake.dir, "gh-search-page-limit"), []byte(fmt.Sprint(limit)), 0o644); err != nil {
		t.Fatalf("write search page limit: %v", err)
	}
}

func (fake *fakeGHGraphQL) LimitMergeAliases(t *testing.T, limit int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(fake.dir, "gh-merge-limit"), []byte(fmt.Sprint(limit)), 0o644); err != nil {
		t.Fatalf("write merge alias limit: %v", err)
	}
}

func (fake *fakeGHGraphQL) registerMergeStates(t *testing.T, stdout string) string {
	t.Helper()
	var response map[string]any
	if json.Unmarshal([]byte(stdout), &response) != nil {
		return stdout
	}
	data, _ := response["data"].(map[string]any)
	search, _ := data["search"].(map[string]any)
	nodes, _ := search["nodes"].([]any)
	if len(nodes) == 0 {
		return stdout
	}
	for _, value := range nodes {
		node, _ := value.(map[string]any)
		id, _ := node["id"].(string)
		if id == "" {
			continue
		}
		mergeState := map[string]any{"id": id, "headRefOid": node["headRefOid"], "reviewDecision": node["reviewDecision"], "mergeable": node["mergeable"], "mergeStateStatus": node["mergeStateStatus"], "commits": node["commits"]}
		encoded, err := json.Marshal(mergeState)
		if err != nil {
			t.Fatalf("encode merge state fixture: %v", err)
		}
		if err := os.WriteFile(filepath.Join(fake.dir, "gh-merge", id+".json"), encoded, 0o644); err != nil {
			t.Fatalf("write merge state fixture: %v", err)
		}
		delete(node, "reviewDecision")
		delete(node, "mergeable")
		delete(node, "mergeStateStatus")
		stripCheckContextNodes(node)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("encode status fixture: %v", err)
	}
	return string(encoded)
}

func stripCheckContextNodes(node map[string]any) {
	commits, _ := node["commits"].(map[string]any)
	commitNodes, _ := commits["nodes"].([]any)
	stripped := []any{}
	for _, value := range commitNodes {
		entry, _ := value.(map[string]any)
		commit, _ := entry["commit"].(map[string]any)
		copied := map[string]any{"oid": commit["oid"], "statusCheckRollup": nil}
		if rollup, ok := commit["statusCheckRollup"].(map[string]any); ok {
			contexts, _ := rollup["contexts"].(map[string]any)
			copied["statusCheckRollup"] = map[string]any{"state": rollup["state"], "contexts": map[string]any{"totalCount": contexts["totalCount"]}}
		}
		stripped = append(stripped, map[string]any{"commit": copied})
	}
	if commits != nil {
		node["commits"] = map[string]any{"nodes": stripped}
	}
}

func serveFakeGHMergeQuery(dir string, inputPath string) int {
	contents, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake gh: read input: %v\n", err)
		return 1
	}
	var request struct {
		Variables map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(contents, &request); err != nil {
		fmt.Fprintf(os.Stderr, "fake gh: parse input: %v\n", err)
		return 1
	}
	ids := []string{}
	for index := 0; ; index++ {
		id, ok := request.Variables[fmt.Sprintf("id%d", index)].(string)
		if !ok {
			break
		}
		ids = append(ids, id)
	}
	if strings.Contains(string(contents), "reviewThreads") {
		data := map[string]any{}
		for index, id := range ids {
			data[fmt.Sprintf("pr%d", index)] = map[string]any{
				"id":                       id,
				"latestOpinionatedReviews": map[string]any{"totalCount": 0, "nodes": []any{}},
				"timelineItems":            map[string]any{"pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil}, "nodes": []any{}},
				"reviewThreads":            map[string]any{"totalCount": 0, "nodes": []any{}},
			}
		}
		encoded, _ := json.Marshal(map[string]any{"data": data})
		fmt.Print(string(encoded))
		return 0
	}
	if limit, err := os.ReadFile(filepath.Join(dir, "gh-merge-limit")); err == nil {
		var max int
		if _, scanErr := fmt.Sscan(string(limit), &max); scanErr == nil && len(ids) > max {
			if raw, err := os.ReadFile(filepath.Join(dir, "gh-timeout-delay")); err == nil {
				if delay, err := time.ParseDuration(string(raw)); err == nil {
					time.Sleep(delay)
				}
			}
			fmt.Fprintln(os.Stderr, "gh: HTTP 502: 502 Bad Gateway (https://api.github.com/graphql)")
			return 1
		}
	}
	data := map[string]any{}
	for index, id := range ids {
		var node any
		if raw, err := os.ReadFile(filepath.Join(dir, "gh-merge", id+".json")); err == nil {
			node = json.RawMessage(raw)
		}
		data[fmt.Sprintf("pr%d", index)] = node
	}
	encoded, err := json.Marshal(map[string]any{"data": data})
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake gh: encode response: %v\n", err)
		return 1
	}
	fmt.Print(string(encoded))
	return 0
}

func (fake *fakeGHGraphQL) mergeBatchStarted(size int) bool {
	inputs, _ := filepath.Glob(filepath.Join(fake.dir, "gh-graphql-inputs", "*.json"))
	for _, path := range inputs {
		contents, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(contents), "mergeStateStatus") && strings.Count(string(contents), "node(id:") == size {
			return true
		}
	}
	return false
}
