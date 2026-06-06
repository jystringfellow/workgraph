package facts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoogleMailConnectOAuthUsesPKCEAndStoresConnectorConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "mail", "connect", "google",
		"--home", homeDir,
		"--no-browser",
		"--client-id", "client-id",
		"--redirect-uri", "http://127.0.0.1:2727/mail/google/callback",
		"--state", "fixed-state",
	)
	if err != nil {
		t.Fatalf("workgraph mail connect URL failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Google Mail OAuth authorization URL") {
		t.Fatalf("expected authorization guidance, got:\n%s", output)
	}
	authorizationURL := mailAuthorizationURL(t, string(output))
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "accounts.google.com" || parsed.Path != "/o/oauth2/v2/auth" {
		t.Fatalf("expected Google OAuth authorize URL, got %s", authorizationURL)
	}
	query := parsed.Query()
	if query.Get("client_id") != "client-id" {
		t.Fatalf("expected client id in authorization URL, got %q", query.Get("client_id"))
	}
	if query.Get("redirect_uri") != "http://127.0.0.1:2727/mail/google/callback" {
		t.Fatalf("expected redirect URI in authorization URL, got %q", query.Get("redirect_uri"))
	}
	if query.Get("state") != "fixed-state" {
		t.Fatalf("expected state in authorization URL, got %q", query.Get("state"))
	}
	if query.Get("response_type") != "code" {
		t.Fatalf("expected authorization code response type, got %q", query.Get("response_type"))
	}
	if query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("expected PKCE challenge in authorization URL, got %q / %q", query.Get("code_challenge"), query.Get("code_challenge_method"))
	}
	if query.Get("access_type") != "offline" {
		t.Fatalf("expected offline access for refresh token, got %q", query.Get("access_type"))
	}
	if query.Get("include_granted_scopes") != "true" {
		t.Fatalf("expected incremental Google authorization, got %q", query.Get("include_granted_scopes"))
	}
	if !strings.Contains(query.Get("scope"), "https://www.googleapis.com/auth/gmail.readonly") {
		t.Fatalf("expected gmail.readonly scope, got %q", query.Get("scope"))
	}
	if _, err := os.Stat(filepath.Join(homeDir, "mail.json")); !os.IsNotExist(err) {
		t.Fatalf("expected mail config not to be written before code exchange, stat err: %v", err)
	}

	var tokenRequestForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/token" {
			t.Fatalf("unexpected token server path %s", request.URL.Path)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse token request form: %v", err)
		}
		tokenRequestForm = request.Form
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "access_token": "mail-access-token",
  "refresh_token": "mail-refresh-token",
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "https://www.googleapis.com/auth/gmail.readonly"
}`))
	}))
	defer server.Close()

	output, err = runworkgraph(t, repoRoot, "mail", "connect", "google",
		"--home", homeDir,
		"--client-id", "client-id",
		"--redirect-uri", "http://127.0.0.1:2727/mail/google/callback",
		"--code", "oauth-code",
		"--code-verifier", "manual-code-verifier",
		"--state", "fixed-state",
		"--expected-state", "fixed-state",
		"--mail-token-url", server.URL+"/token",
		"--mail-api-base", "https://gmail.test",
	)
	if err != nil {
		t.Fatalf("workgraph mail connect exchange failed: %v\n%s", err, output)
	}
	if tokenRequestForm.Get("grant_type") != "authorization_code" {
		t.Fatalf("expected authorization_code grant, got %q", tokenRequestForm.Get("grant_type"))
	}
	if tokenRequestForm.Get("code") != "oauth-code" {
		t.Fatalf("expected oauth code in token request, got %q", tokenRequestForm.Get("code"))
	}
	if tokenRequestForm.Get("client_id") != "client-id" || tokenRequestForm.Get("code_verifier") != "manual-code-verifier" {
		t.Fatalf("expected PKCE client id and verifier in token request, got %#v", tokenRequestForm)
	}
	if _, ok := tokenRequestForm["client_secret"]; ok {
		t.Fatalf("expected no client_secret field in mail token request, got %#v", tokenRequestForm["client_secret"])
	}
	if !strings.Contains(string(output), "Google Mail connected") {
		t.Fatalf("expected connected message, got:\n%s", output)
	}

	configPath := filepath.Join(homeDir, "mail.json")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("expected mail config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected mail config permissions 0600, got %v", got)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read mail config: %v", err)
	}
	var stored struct {
		Google struct {
			AccessToken  string   `json:"access_token"`
			RefreshToken string   `json:"refresh_token"`
			TokenType    string   `json:"token_type"`
			Scopes       []string `json:"scopes"`
			APIBaseURL   string   `json:"api_base_url"`
			ClientID     string   `json:"client_id"`
			TokenURL     string   `json:"token_url"`
		} `json:"google"`
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatalf("parse mail config: %v", err)
	}
	if stored.Google.AccessToken != "mail-access-token" || stored.Google.RefreshToken != "mail-refresh-token" || stored.Google.TokenType != "Bearer" {
		t.Fatalf("expected stored mail tokens, got %#v", stored.Google)
	}
	if len(stored.Google.Scopes) != 1 || stored.Google.Scopes[0] != "https://www.googleapis.com/auth/gmail.readonly" {
		t.Fatalf("expected stored Gmail scope, got %#v", stored.Google.Scopes)
	}
	if stored.Google.APIBaseURL != "https://gmail.test" {
		t.Fatalf("expected stored API base URL, got %q", stored.Google.APIBaseURL)
	}
	if stored.Google.ClientID != "client-id" || stored.Google.TokenURL != server.URL+"/token" {
		t.Fatalf("expected stored OAuth metadata, got %#v", stored.Google)
	}
}

func TestMicrosoftMailConnectOAuthUsesPKCEAndStoresConnectorConfig(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	output, err := runworkgraph(t, repoRoot, "mail", "connect", "microsoft",
		"--home", homeDir,
		"--no-browser",
		"--client-id", "microsoft-client-id",
		"--redirect-uri", "http://localhost:2727/mail/microsoft/callback",
		"--state", "fixed-state",
	)
	if err != nil {
		t.Fatalf("workgraph mail connect URL failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Microsoft Mail OAuth authorization URL") {
		t.Fatalf("expected authorization guidance, got:\n%s", output)
	}
	authorizationURL := mailAuthorizationURL(t, string(output))
	parsed, err := url.Parse(authorizationURL)
	if err != nil {
		t.Fatalf("parse authorization url: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "login.microsoftonline.com" || parsed.Path != "/common/oauth2/v2.0/authorize" {
		t.Fatalf("expected Microsoft OAuth authorize URL, got %s", authorizationURL)
	}
	query := parsed.Query()
	if query.Get("client_id") != "microsoft-client-id" {
		t.Fatalf("expected client id in authorization URL, got %q", query.Get("client_id"))
	}
	if query.Get("redirect_uri") != "http://localhost:2727/mail/microsoft/callback" {
		t.Fatalf("expected redirect URI in authorization URL, got %q", query.Get("redirect_uri"))
	}
	if query.Get("state") != "fixed-state" {
		t.Fatalf("expected state in authorization URL, got %q", query.Get("state"))
	}
	if query.Get("response_type") != "code" {
		t.Fatalf("expected authorization code response type, got %q", query.Get("response_type"))
	}
	if query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("expected PKCE challenge in authorization URL, got %q / %q", query.Get("code_challenge"), query.Get("code_challenge_method"))
	}
	scope := query.Get("scope")
	for _, expected := range []string{
		"openid",
		"profile",
		"email",
		"offline_access",
		"https://graph.microsoft.com/Mail.Read",
		"https://graph.microsoft.com/Mail.Read.Shared",
	} {
		if !strings.Contains(scope, expected) {
			t.Fatalf("expected Microsoft Mail scope %q, got %q", expected, scope)
		}
	}
	if _, err := os.Stat(filepath.Join(homeDir, "mail.json")); !os.IsNotExist(err) {
		t.Fatalf("expected mail config not to be written before code exchange, stat err: %v", err)
	}

	var tokenRequestForm url.Values
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/token" {
			t.Fatalf("unexpected token server path %s", request.URL.Path)
		}
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse token request form: %v", err)
		}
		tokenRequestForm = request.Form
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "access_token": "microsoft-mail-access-token",
  "refresh_token": "microsoft-mail-refresh-token",
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "openid profile email offline_access https://graph.microsoft.com/Mail.Read https://graph.microsoft.com/Mail.Read.Shared"
}`))
	}))
	defer server.Close()

	output, err = runworkgraph(t, repoRoot, "mail", "connect", "microsoft",
		"--home", homeDir,
		"--client-id", "microsoft-client-id",
		"--redirect-uri", "http://localhost:2727/mail/microsoft/callback",
		"--code", "oauth-code",
		"--code-verifier", "manual-code-verifier",
		"--state", "fixed-state",
		"--expected-state", "fixed-state",
		"--mail-token-url", server.URL+"/token",
		"--mail-api-base", "https://graph.test",
	)
	if err != nil {
		t.Fatalf("workgraph mail connect exchange failed: %v\n%s", err, output)
	}
	if tokenRequestForm.Get("grant_type") != "authorization_code" {
		t.Fatalf("expected authorization_code grant, got %q", tokenRequestForm.Get("grant_type"))
	}
	if tokenRequestForm.Get("code") != "oauth-code" {
		t.Fatalf("expected oauth code in token request, got %q", tokenRequestForm.Get("code"))
	}
	if tokenRequestForm.Get("client_id") != "microsoft-client-id" || tokenRequestForm.Get("code_verifier") != "manual-code-verifier" {
		t.Fatalf("expected PKCE client id and verifier in token request, got %#v", tokenRequestForm)
	}
	if _, ok := tokenRequestForm["client_secret"]; ok {
		t.Fatalf("expected no client_secret field in mail token request, got %#v", tokenRequestForm["client_secret"])
	}
	if !strings.Contains(string(output), "Microsoft Mail connected") {
		t.Fatalf("expected connected message, got:\n%s", output)
	}

	configPath := filepath.Join(homeDir, "mail.json")
	info, err := os.Stat(configPath)
	if err != nil {
		t.Fatalf("expected mail config: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("expected mail config permissions 0600, got %v", got)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read mail config: %v", err)
	}
	var stored struct {
		Microsoft struct {
			AccessToken  string   `json:"access_token"`
			RefreshToken string   `json:"refresh_token"`
			TokenType    string   `json:"token_type"`
			Scopes       []string `json:"scopes"`
			APIBaseURL   string   `json:"api_base_url"`
			ClientID     string   `json:"client_id"`
			TokenURL     string   `json:"token_url"`
		} `json:"microsoft"`
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatalf("parse mail config: %v", err)
	}
	if stored.Microsoft.AccessToken != "microsoft-mail-access-token" || stored.Microsoft.RefreshToken != "microsoft-mail-refresh-token" || stored.Microsoft.TokenType != "Bearer" {
		t.Fatalf("expected stored mail tokens, got %#v", stored.Microsoft)
	}
	for _, expected := range []string{"https://graph.microsoft.com/Mail.Read", "https://graph.microsoft.com/Mail.Read.Shared"} {
		if !stringSliceContains(stored.Microsoft.Scopes, expected) {
			t.Fatalf("expected stored Microsoft Mail scope %q, got %#v", expected, stored.Microsoft.Scopes)
		}
	}
	if stored.Microsoft.APIBaseURL != "https://graph.test" {
		t.Fatalf("expected stored API base URL, got %q", stored.Microsoft.APIBaseURL)
	}
	if stored.Microsoft.ClientID != "microsoft-client-id" || stored.Microsoft.TokenURL != server.URL+"/token" {
		t.Fatalf("expected stored OAuth metadata, got %#v", stored.Microsoft)
	}
}

func TestMailConnectPreservesOtherProviderSettings(t *testing.T) {
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, ".workgraph")
	repoRoot := repoRoot(t)
	if output, err := runworkgraph(t, repoRoot, "init", "--home", homeDir); err != nil {
		t.Fatalf("workgraph init failed: %v\n%s", err, output)
	}

	microsoftServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "access_token": "microsoft-mail-access-token",
  "refresh_token": "microsoft-mail-refresh-token",
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "openid profile email offline_access https://graph.microsoft.com/Mail.Read https://graph.microsoft.com/Mail.Read.Shared"
}`))
	}))
	defer microsoftServer.Close()

	if output, err := runworkgraph(t, repoRoot, "mail", "connect", "microsoft",
		"--home", homeDir,
		"--client-id", "microsoft-client-id",
		"--redirect-uri", "http://localhost:2727/mail/microsoft/callback",
		"--code", "microsoft-code",
		"--code-verifier", "microsoft-verifier",
		"--state", "fixed-state",
		"--expected-state", "fixed-state",
		"--mail-token-url", microsoftServer.URL,
	); err != nil {
		t.Fatalf("workgraph microsoft mail connect failed: %v\n%s", err, output)
	}

	googleServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
  "access_token": "google-mail-access-token",
  "refresh_token": "google-mail-refresh-token",
  "token_type": "Bearer",
  "expires_in": 3600,
  "scope": "https://www.googleapis.com/auth/gmail.readonly"
}`))
	}))
	defer googleServer.Close()

	if output, err := runworkgraph(t, repoRoot, "mail", "connect", "google",
		"--home", homeDir,
		"--client-id", "google-client-id",
		"--redirect-uri", "http://127.0.0.1:2727/mail/google/callback",
		"--code", "google-code",
		"--code-verifier", "google-verifier",
		"--state", "fixed-state",
		"--expected-state", "fixed-state",
		"--mail-token-url", googleServer.URL,
	); err != nil {
		t.Fatalf("workgraph google mail connect failed: %v\n%s", err, output)
	}

	contents, err := os.ReadFile(filepath.Join(homeDir, "mail.json"))
	if err != nil {
		t.Fatalf("read mail config: %v", err)
	}
	var stored struct {
		Google struct {
			AccessToken string `json:"access_token"`
		} `json:"google"`
		Microsoft struct {
			AccessToken string `json:"access_token"`
		} `json:"microsoft"`
	}
	if err := json.Unmarshal(contents, &stored); err != nil {
		t.Fatalf("parse mail config: %v", err)
	}
	if stored.Google.AccessToken != "google-mail-access-token" {
		t.Fatalf("expected Google Mail settings to be stored, got %#v", stored.Google)
	}
	if stored.Microsoft.AccessToken != "microsoft-mail-access-token" {
		t.Fatalf("expected Microsoft Mail settings to be preserved, got %#v", stored.Microsoft)
	}
}

func mailAuthorizationURL(t *testing.T, output string) string {
	t.Helper()

	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "https://") {
			return line
		}
	}
	t.Fatalf("expected authorization URL in output:\n%s", output)
	return ""
}

func stringSliceContains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
