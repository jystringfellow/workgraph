package main

import "testing"

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
