package facts

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for _, sub := range []string{fake.queueDir, filepath.Join(dir, "gh-graphql-inputs")} {
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
  n=$(cat "$dir/gh-graphql-count" 2>/dev/null || echo 0)
  n=$((n + 1))
  echo "$n" > "$dir/gh-graphql-count"
  input=""
  prev=""
  for arg in "$@"; do
    if [ "$prev" = "--input" ]; then input="$arg"; fi
    prev="$arg"
  done
  if [ -n "$input" ]; then cp "$input" "$dir/gh-graphql-inputs/$n.json"; fi
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
