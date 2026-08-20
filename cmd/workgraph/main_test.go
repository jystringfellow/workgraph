package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestGitHubLoginsFromAuthStatusParsesLocalAccountNames(t *testing.T) {
	output := `github.com
  X Failed to log in to github.com account jystringfellow (default)
  - Active account: true
  - The token in default is invalid.
`

	logins := githubLoginsFromAuthStatus(output)
	if len(logins) != 1 || logins[0] != "jystringfellow" {
		t.Fatalf("expected GitHub login from auth status, got %#v", logins)
	}
}

func TestVersionUsesInjectedReleaseIdentity(t *testing.T) {
	oldVersion, oldCommit, oldBuildDate := version, commit, buildDate
	t.Cleanup(func() {
		version, commit, buildDate = oldVersion, oldCommit, oldBuildDate
	})
	version = "v1.2.3"
	commit = "0123456789abcdef0123456789abcdef01234567"
	buildDate = "2026-08-20T12:34:56Z"

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := run([]string{"version"}, &stdout, &stderr); code != 0 {
		t.Fatalf("version exit code %d: %s", code, stderr.String())
	}
	want := "workgraph v1.2.3\ncommit: 0123456789abcdef0123456789abcdef01234567\nbuilt: 2026-08-20T12:34:56Z\n"
	if stdout.String() != want {
		t.Fatalf("version output:\n%s\nwant:\n%s", stdout.String(), want)
	}
	if strings.Contains(stdout.String(), "/") {
		t.Fatalf("version output exposed a path: %s", stdout.String())
	}
}
