package facts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

const systemOneContainer = "dock-team"

var systemOneCatalogSlugs = []string{"atlas-bot", "harbor-harness", "orbit-segments"}

func TestSystemOneProfileIsConfiguredWithoutStoringItsKey(t *testing.T) {
	homeDir := filepath.Join(t.TempDir(), ".workgraph")
	if output, err := runworkgraph(t, repoRoot(t), "init", "--home", homeDir); err != nil {
		t.Fatalf("init: %v\n%s", err, output)
	}
	t.Setenv("WG_DECISION_KEY", "sk-decision-secret-value-0123456789")

	output, err := runworkgraph(t, repoRoot(t), "llm", "add", "jev", "--home", homeDir, "--provider", "system-one", "--base-url", "https://api.typesafe.example/", "--model", "jev-latest", "--api-key-env", "WG_DECISION_KEY")
	if err != nil {
		t.Fatalf("llm add system-one: %v\n%s", err, output)
	}
	stored, err := os.ReadFile(filepath.Join(homeDir, "llm.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Profiles map[string]map[string]string `json:"profiles"`
	}
	if err := json.Unmarshal(stored, &config); err != nil {
		t.Fatal(err)
	}
	if got := config.Profiles["jev"]; got["provider"] != "system-one" || got["base_url"] != "https://api.typesafe.example" || got["model"] != "jev-latest" || got["api_key_env"] != "WG_DECISION_KEY" {
		t.Fatalf("unexpected stored profile: %+v", got)
	}
	if strings.Contains(string(stored), "sk-decision-secret") {
		t.Fatalf("llm.json stored the API key:\n%s", stored)
	}

	for _, command := range [][]string{{"llm", "list"}, {"llm", "doctor"}} {
		output, err := runworkgraph(t, repoRoot(t), append(command, "--home", homeDir)...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", command, err, output)
		}
		for _, expected := range []string{"jev: system-one jev-latest", "api.typesafe.example"} {
			if !strings.Contains(string(output), expected) {
				t.Fatalf("%v omitted %q:\n%s", command, expected, output)
			}
		}
		for _, leaked := range []string{"sk-decision-secret", "WG_DECISION_KEY"} {
			if strings.Contains(string(output), leaked) {
				t.Fatalf("%v printed %q:\n%s", command, leaked, output)
			}
		}
	}

	if output, err := runworkgraph(t, repoRoot(t), "llm", "add", "no-url", "--home", homeDir, "--provider", "system-one", "--model", "jev-latest"); err == nil || !strings.Contains(string(output), "system-one llm base URL is required") {
		t.Fatalf("expected a missing base URL to be refused: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), "llm", "add", "fragment", "--home", homeDir, "--provider", "system-one", "--base-url", "https://api.typesafe.example/v1#frag", "--model", "jev-latest"); err == nil || !strings.Contains(string(output), "must not have a fragment") {
		t.Fatalf("expected a base URL with a fragment to be refused: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), "llm", "add", "bridged", "--home", homeDir, "--provider", "system-one", "--base-url", "http://127.0.0.1:8000", "--model", "clef-flash", "--execution", "bridge"); err == nil || !strings.Contains(string(output), "system-one profiles run direct only") {
		t.Fatalf("expected bridge execution to be refused: %v\n%s", err, output)
	}
	if output, err := runworkgraph(t, repoRoot(t), "llm", "test", "--home", homeDir, "--profile", "jev"); err == nil || !strings.Contains(string(output), "system-one profiles answer decision questions only") {
		t.Fatalf("expected llm test to refuse a decision-only profile: %v\n%s", err, output)
	}
}

func TestSystemOneAssociateMapsOpaqueChoicesToLinksWithReturnedProbability(t *testing.T) {
	home := initSystemOneHome(t, true)
	fake := newSystemOneFake(t)
	t.Setenv("WG_DECISION_KEY", "local-key-0123456789")
	configureSystemOneProfile(t, home, "http://127.0.0.1:8000", "WG_DECISION_KEY", "slack")
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	units := insertAssociateThreads(t, home, now.Add(-6*time.Hour), "C1", systemOneContainer, 8)
	build := systemOneChoice("build", 0.9, map[string]any{"build": 0.9, "fyi": 0.1})
	fake.answer("thread 0 in C1", systemOneChoice("p1", 0.5, map[string]any{"p1": 0.97, "no_project": 0.03}), build)
	fake.answer("thread 1 in C1", systemOneChoice("no_project", 0.2, map[string]any{"p1": 0.4, "no_project": 0.6}), systemOneChoice("fyi", 0.8, map[string]any{"fyi": 0.8, "build": 0.2}))
	fake.answer("thread 2 in C1", systemOneChoice("atlas-bot", 0.99, map[string]any{"atlas-bot": 0.99}), build)
	fake.answer("thread 3 in C1", systemOneChoice("p1", 0.9, map[string]any{"no_project": 0.1}), build)
	fake.answer("thread 4 in C1", systemOneChoice("p1", 0.9, map[string]any{"p1": nil}), build)
	fake.answer("thread 5 in C1", systemOneChoice("p1", 0.9, map[string]any{"p1": 1.5}), build)
	fake.answer("thread 6 in C1", systemOneChoice("p2", 0.99, map[string]any{"p2": 0.99}), systemOneChoice("gossip", 1, map[string]any{"gossip": 1}))
	fake.answer("thread 7 in C1", systemOneChoice("p2", 0.99, map[string]any{"p2": 0.99}), systemOneChoice("build", 1, map[string]any{"build": nil}))

	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client, Now: now})
	if err != nil {
		t.Fatalf("associate: %v", err)
	}
	requests := fake.received()
	if len(requests) != 8 {
		t.Fatalf("expected one System One request per unit, got %d", len(requests))
	}
	first := requests[0]
	if first.method != http.MethodPost || first.url != "http://127.0.0.1:8000/v1/systemone" || first.authorization != "Bearer local-key-0123456789" || first.contentType != "application/json" {
		t.Fatalf("unexpected request line or headers: %+v", first)
	}
	if first.body.Model != "clef-flash" || !strings.Contains(first.body.State, "thread 0 in C1") || !strings.Contains(first.body.State, systemOneContainer) {
		t.Fatalf("unexpected model or state: %+v", first.body)
	}
	if len(first.body.Questions) != 2 {
		t.Fatalf("expected a project and a kind question, got %+v", first.body.Questions)
	}
	project, kind := first.body.Questions["project"], first.body.Questions["kind"]
	if project.Type != "choice" || strings.TrimSpace(project.Instructions) == "" || kind.Type != "choice" || strings.TrimSpace(kind.Instructions) == "" {
		t.Fatalf("expected two choice questions with instructions, got %+v", first.body.Questions)
	}
	if got := sortedKeys(project.Criteria); !reflect.DeepEqual(got, []string{"no_project", "p1", "p2", "p3"}) {
		t.Fatalf("expected opaque options for the candidate catalog plus no_project, got %v", got)
	}
	if !strings.Contains(project.Criteria["p1"], "Atlas Bot") || !strings.Contains(project.Criteria["p2"], "Harbor Harness") || !strings.Contains(project.Criteria["p2"], "harbor") {
		t.Fatalf("expected options described from the catalog, got %+v", project.Criteria)
	}
	for _, request := range requests {
		for _, slug := range systemOneCatalogSlugs {
			if strings.Contains(request.raw, slug) {
				t.Fatalf("request sent the project slug %q:\n%s", slug, request.raw)
			}
		}
	}
	if got := sortedKeys(kind.Criteria); !reflect.DeepEqual(got, []string{"advice", "build", "coordination", "decision", "fyi", "review", "unblock"}) {
		t.Fatalf("expected the seven link kinds, got %v", got)
	}

	store := workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}
	chosen := currentProjectLink(t, store, units[0].ID)
	if chosen.Target != "project" || chosen.Project != "atlas-bot" || chosen.Confidence != 0.97 || chosen.Kind != "build" || chosen.State != "applied" || !chosen.Auto || chosen.Source != "llm" || chosen.Profile != "decisions" {
		t.Fatalf("expected an auto-applied link at the returned probability, got %+v", chosen)
	}
	if chosen.Summary != "thread 0 in C1" || !reflect.DeepEqual(chosen.Evidence, units[0].EventIDs) {
		t.Fatalf("expected the first event summary and every member as evidence, got %+v", chosen)
	}
	none := currentProjectLink(t, store, units[1].ID)
	if none.Target != "none" || none.Confidence != 0.6 || none.Kind != "fyi" || none.State != "proposed" {
		t.Fatalf("expected a proposed no-project link at 0.6, got %+v", none)
	}
	if result.Applied != 1 || result.Proposed != 1 || len(result.Discarded) != 6 {
		t.Fatalf("expected one applied, one proposed, six discarded, got %+v", result)
	}
	reasons := map[string]string{}
	for _, discard := range result.Discarded {
		reasons[discard.UnitID] = discard.Reason
	}
	for index, expected := range map[int]string{
		2: `project choice "atlas-bot" was not offered`,
		3: `project probability for choice "p1" is not a number in [0, 1]`,
		4: `project probability for choice "p1" is not a number in [0, 1]`,
		5: `project probability for choice "p1" is not a number in [0, 1]`,
		6: `kind choice "gossip" was not offered`,
		7: `kind probability for choice "build" is not a number in [0, 1]`,
	} {
		if reasons[units[index].ID] != expected {
			t.Fatalf("expected unit %d to be discarded with %q, got %q", index, expected, reasons[units[index].ID])
		}
	}
	for _, unit := range units[2:] {
		if links, _ := workgraph.ReadProjectLinks(store, workgraph.ProjectLinksQuery{UnitID: unit.ID}); len(links) != 0 {
			t.Fatalf("an invalid answer stored a link for %s: %+v", unit.ID, links)
		}
	}
}

func TestSystemOneAssociateDryRunPrintsRequestsWithoutCallingTheModel(t *testing.T) {
	home := initSystemOneHome(t, true)
	fake := newSystemOneFake(t)
	configureSystemOneProfile(t, home, "http://localhost:11434", "", "slack")
	insertAssociateThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "C1", systemOneContainer, 1)

	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client, DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if calls := len(fake.received()); calls != 0 {
		t.Fatalf("dry run made %d requests", calls)
	}
	for _, expected := range []string{"LLM associate dry run", "Provider: system-one", "Destination: http://localhost:11434", "Units: 1", "Requests:", `"questions"`, `"no_project"`, `"p1"`, "Atlas Bot", "thread 0 in C1"} {
		if !strings.Contains(result.Message, expected) {
			t.Fatalf("dry run omitted %q:\n%s", expected, result.Message)
		}
	}
	if links, _ := workgraph.ReadProjectLinks(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, workgraph.ProjectLinksQuery{}); len(links) != 0 {
		t.Fatalf("dry run stored links: %+v", links)
	}
}

func TestSystemOneChoiceAcceptsTheOptionLimitAndSkipsUnitsBeyondIt(t *testing.T) {
	home := initSystemOneHome(t, true)
	for i := 0; i < 251; i++ {
		writeProjectMemory(t, home.MemoryDir, fmt.Sprintf("catalog-entry-%03d", i), fmt.Sprintf("# Catalog Entry %d\n", i))
	}
	fake := newSystemOneFake(t)
	configureSystemOneProfile(t, home, "http://127.0.0.1:8000", "", "slack")
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	insertAssociateThreads(t, home, now.Add(-6*time.Hour), "C1", systemOneContainer, 1)
	fake.answer("thread 0 in C1", systemOneChoice("p1", 0.9, map[string]any{"p1": 0.8}), systemOneChoice("build", 1, map[string]any{"build": 1}))

	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client, Now: now})
	if err != nil {
		t.Fatalf("associate at the limit: %v", err)
	}
	requests := fake.received()
	if len(requests) != 1 || len(requests[0].body.Questions["project"].Criteria) != 255 || result.Proposed != 1 || len(result.Discarded) != 0 {
		t.Fatalf("expected 254 candidates plus no_project to be sent as 255 options, got %d requests %+v", len(requests), result)
	}

	writeProjectMemory(t, home.MemoryDir, "catalog-entry-251", "# Catalog Entry 251\n")
	later := insertAssociateThreads(t, home, now.Add(-5*time.Hour), "C2", systemOneContainer, 1)
	result, err = workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client, Now: now})
	if err != nil {
		t.Fatalf("associate over the limit: %v", err)
	}
	if calls := len(fake.received()); calls != 0 {
		t.Fatalf("sent a unit whose choice exceeds the option limit: %d calls", calls)
	}
	if len(result.Discarded) != 1 || result.Discarded[0].UnitID != later[0].ID || !strings.Contains(result.Discarded[0].Reason, "256 options") || !strings.Contains(result.Discarded[0].Reason, "255") {
		t.Fatalf("expected the unit to be skipped with an option-limit reason, got %+v", result.Discarded)
	}

	empty := initSystemOneHome(t, false)
	configureSystemOneProfile(t, empty, "http://127.0.0.1:8000", "", "slack")
	emptyUnits := insertAssociateThreads(t, empty, now.Add(-6*time.Hour), "C3", systemOneContainer, 1)
	result, err = workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: empty.HomeDir, MemoryDir: empty.MemoryDir, HTTPClient: fake.client, Now: now})
	if err != nil {
		t.Fatalf("associate without candidates: %v", err)
	}
	if calls := len(fake.received()); calls != 0 {
		t.Fatalf("sent a unit with no candidate project: %d calls", calls)
	}
	if len(result.Discarded) != 1 || result.Discarded[0].UnitID != emptyUnits[0].ID || !strings.Contains(result.Discarded[0].Reason, "no candidate project") {
		t.Fatalf("expected the unit to be skipped for having no candidates, got %+v", result.Discarded)
	}
}

func TestSystemOneHostedBaseURLNeedsOptInAndFiltersEveryOutboundField(t *testing.T) {
	home := initSystemOneHome(t, true)
	fake := newSystemOneFake(t)
	configureSystemOneProfile(t, home, "https://decisions.example.net", "", "slack")
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	start := now.Add(-6 * time.Hour)
	insertEvent(t, home.DatabasePath, associateSlackEvent("C1", fmt.Sprintf("%d.000100", start.Unix()), "channel", systemOneContainer, "Sam", "rotate ghp_abcdefghijklmnopqrstuvwxyz0123 before the Orbit Segments launch", start))
	fake.answer("rotate", systemOneChoice("p1", 0.9, map[string]any{"p1": 0.9, "no_project": 0.1}), systemOneChoice("build", 1, map[string]any{"build": 1}))
	config := workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client, Now: now}

	if _, err := workgraph.AssociateWithLLM(config); err == nil || !strings.Contains(err.Error(), "hosted LLM use is not enabled") {
		t.Fatalf("expected a hosted base URL to need opt-in, got %v", err)
	}
	if calls := len(fake.received()); calls != 0 {
		t.Fatalf("sent context to a hosted System One host without opt-in: %d calls", calls)
	}
	if _, err := workgraph.EnableHostedLLM(workgraph.LLMHostedConfig{HomeDir: home.HomeDir}); err != nil {
		t.Fatal(err)
	}
	managedPath := filepath.Join(t.TempDir(), "managed-settings.json")
	writeSystemOneManagedSettings(t, managedPath, `{"version":1,"llm":{"outbound_filter":{"sensitive_patterns":{"value":["Orbit Segments","orbit-segments"]}}}}`)
	defer workgraph.SetManagedSettingsPathForTest(managedPath)()
	if _, err := workgraph.AssociateWithLLM(config); err != nil {
		t.Fatalf("associate after opt-in: %v", err)
	}
	requests := fake.received()
	if len(requests) != 1 || requests[0].url != "https://decisions.example.net/v1/systemone" || requests[0].authorization != "" {
		t.Fatalf("expected one unauthenticated hosted request, got %+v", requests)
	}
	for _, leaked := range []string{"ghp_abcdefghijklmnopqrstuvwxyz0123", "Orbit Segments", "orbit-segments"} {
		if strings.Contains(requests[0].raw, leaked) {
			t.Fatalf("hosted request leaked %q past the outbound filter:\n%s", leaked, requests[0].raw)
		}
	}
	if !strings.Contains(requests[0].body.State, "[REDACTED:github-token]") || !strings.Contains(requests[0].body.State, "[REDACTED:managed-pattern]") || !strings.Contains(requests[0].body.Questions["project"].Criteria["p3"], "[REDACTED:managed-pattern]") {
		t.Fatalf("expected state and option descriptions to be redacted, got %+v", requests[0].body)
	}

	writeSystemOneManagedSettings(t, managedPath, `{"version":1,"llm":{"hosted_enabled":{"value":false,"locked":true}}}`)
	later := start.Add(time.Hour)
	insertEvent(t, home.DatabasePath, associateSlackEvent("C1", fmt.Sprintf("%d.000100", later.Unix()), "channel", systemOneContainer, "Sam", "rotate the next key", later))
	if _, err := workgraph.AssociateWithLLM(config); err == nil || !strings.Contains(err.Error(), "hosted LLM providers are disabled by managed settings") {
		t.Fatalf("expected managed settings to block a hosted System One host, got %v", err)
	}

	local := initSystemOneHome(t, true)
	configureSystemOneProfile(t, local, "http://[::1]:8000", "", "slack")
	localStart := now.Add(-5 * time.Hour)
	insertEvent(t, local.DatabasePath, associateSlackEvent("C1", fmt.Sprintf("%d.000100", localStart.Unix()), "channel", systemOneContainer, "Sam", "rotate ghp_abcdefghijklmnopqrstuvwxyz0123 locally", localStart))
	if _, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: local.HomeDir, MemoryDir: local.MemoryDir, HTTPClient: fake.client, Now: now}); err != nil {
		t.Fatalf("expected a loopback System One host to need no hosted opt-in even under managed hosted disable: %v", err)
	}
	requests = fake.received()
	if len(requests) != 1 || requests[0].url != "http://[::1]:8000/v1/systemone" || !strings.Contains(requests[0].body.State, "ghp_abcdefghijklmnopqrstuvwxyz0123") {
		t.Fatalf("expected one unfiltered loopback request, got %+v", requests)
	}
}

func TestSystemOneDeceptiveLoopbackLookalikesAreHosted(t *testing.T) {
	home := initSystemOneHome(t, true)
	fake := newSystemOneFake(t)
	insertAssociateThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "C1", systemOneContainer, 1)
	for _, baseURL := range []string{"http://localhost.evil.example:8000", "http://127.0.0.1@decisions.example.net", "http://127.0.0.1.nip.io:8000"} {
		if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "decisions", Provider: "system-one", BaseURL: baseURL, Model: "clef-flash"}); err != nil {
			t.Fatalf("add %s: %v", baseURL, err)
		}
		configureSystemOneRoute(t, home, "slack")
		if _, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client}); err == nil || !strings.Contains(err.Error(), "hosted LLM use is not enabled") {
			t.Fatalf("expected %s to be classified as hosted, got %v", baseURL, err)
		}
		if calls := len(fake.received()); calls != 0 {
			t.Fatalf("%s received %d requests without hosted opt-in", baseURL, calls)
		}
	}
}

func TestSystemOneRefusesRedirects(t *testing.T) {
	home := initSystemOneHome(t, true)
	fake := newSystemOneFake(t)
	configureSystemOneProfile(t, home, "http://127.0.0.1:8000", "", "slack")
	units := insertAssociateThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "C1", systemOneContainer, 1)
	fake.redirect = "https://decisions.example.net/v1/systemone"
	fake.answer("thread 0 in C1", systemOneChoice("p1", 0.99, map[string]any{"p1": 0.99}), systemOneChoice("build", 1, map[string]any{"build": 1}))

	_, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client})
	if err == nil || !strings.Contains(err.Error(), "status 307: redirects are refused") {
		t.Fatalf("expected a redirect to fail the run, got %v", err)
	}
	requests := fake.received()
	if len(requests) != 1 || requests[0].url != "http://127.0.0.1:8000/v1/systemone" {
		t.Fatalf("expected the redirect target never to receive the body, got %+v", requests)
	}
	if links, _ := workgraph.ReadProjectLinks(workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, workgraph.ProjectLinksQuery{UnitID: units[0].ID}); len(links) != 0 {
		t.Fatalf("a refused redirect stored links: %+v", links)
	}
}

func TestSystemOneErrorsDoNotDiscloseCredentialsOrURLSecrets(t *testing.T) {
	home := initSystemOneHome(t, true)
	fake := newSystemOneFake(t)
	t.Setenv("WG_DECISION_KEY", "sk-live-secret-0123456789abcdef")
	configureSystemOneProfile(t, home, "http://operator:pa55word-xyz@127.0.0.1:8000/v1?token=qsecret12345", "WG_DECISION_KEY", "slack")
	insertAssociateThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "C1", systemOneContainer, 1)
	secrets := []string{"sk-live-secret-0123456789abcdef", "pa55word-xyz", "qsecret12345"}
	config := workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client}

	fake.failure = func() (*http.Response, error) {
		recorder := httptest.NewRecorder()
		recorder.WriteHeader(http.StatusUnauthorized)
		_, _ = recorder.WriteString("invalid key sk-live-secret-0123456789abcdef for operator:pa55word-xyz at /v1/systemone?token=qsecret12345\x1b[2J")
		return recorder.Result(), nil
	}
	_, err := workgraph.AssociateWithLLM(config)
	if err == nil || !strings.Contains(err.Error(), "http://127.0.0.1:8000: status 401") || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("expected a sanitized status error naming only the host, got %v", err)
	}
	assertNoSystemOneSecrets(t, err.Error(), append(secrets, "\x1b")...)
	if requests := fake.received(); len(requests) != 1 || requests[0].url != "http://operator:pa55word-xyz@127.0.0.1:8000/v1/systemone?token=qsecret12345" {
		t.Fatalf("expected the query to be kept on the endpoint, got %+v", requests)
	}

	fake.failure = func() (*http.Response, error) {
		return nil, errors.New("connection reset by peer")
	}
	_, err = workgraph.AssociateWithLLM(config)
	if err == nil || !strings.Contains(err.Error(), "call system-one profile at http://127.0.0.1:8000: connection reset by peer") {
		t.Fatalf("expected a transport error naming only the host, got %v", err)
	}
	assertNoSystemOneSecrets(t, err.Error(), append(secrets, "/v1")...)

	managedPath := filepath.Join(t.TempDir(), "managed-settings.json")
	writeSystemOneManagedSettings(t, managedPath, `{"version":1,"llm":{"allowed_base_urls":{"value":["https://allowed.example.net"]}}}`)
	defer workgraph.SetManagedSettingsPathForTest(managedPath)()
	doctor, err := workgraph.DoctorLLMProfiles(workgraph.LLMDoctorConfig{HomeDir: home.HomeDir})
	if err == nil || !strings.Contains(doctor.Message, "managed policy: blocked - llm destination \"http://127.0.0.1:8000/v1?REDACTED\" is not allowed by managed settings") {
		t.Fatalf("expected the allowlist rejection to name the destination without secrets, got %v\n%s", err, doctor.Message)
	}
	assertNoSystemOneSecrets(t, doctor.Message, secrets...)
}

func TestSystemOneEndpointKeepsAnExplicitSystemOnePath(t *testing.T) {
	home := initSystemOneHome(t, true)
	fake := newSystemOneFake(t)
	configureSystemOneProfile(t, home, "http://127.0.0.1:8000/providers/microsoft/v1/systemone", "", "slack")
	now := time.Date(2026, 10, 21, 18, 0, 0, 0, time.UTC)
	insertAssociateThreads(t, home, now.Add(-6*time.Hour), "C1", systemOneContainer, 1)
	fake.envelope = true
	fake.answer("thread 0 in C1", systemOneChoice("p1", 0.9, map[string]any{"p1": 0.8, "no_project": 0.2}), systemOneChoice("review", 1, map[string]any{"review": 1}))

	result, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client, Now: now})
	if err != nil {
		t.Fatalf("associate: %v", err)
	}
	requests := fake.received()
	if len(requests) != 1 || requests[0].url != "http://127.0.0.1:8000/providers/microsoft/v1/systemone" {
		t.Fatalf("expected the configured System One path to be used unchanged, got %+v", requests)
	}
	if result.Proposed != 1 || len(result.Discarded) != 0 {
		t.Fatalf("expected a result-wrapped response to be read, got %+v", result)
	}
}

func TestSystemOneProfilesRefuseBridgeExecution(t *testing.T) {
	home := initSystemOneHome(t, true)
	fake := newSystemOneFake(t)
	configureSystemOneProfile(t, home, "http://127.0.0.1:8000", "", "slack")
	insertAssociateThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "C1", systemOneContainer, 1)
	path := filepath.Join(home.HomeDir, "llm.json")
	stored, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.Replace(string(stored), `"provider": "system-one",`, `"provider": "system-one", "execution": "bridge",`, 1)
	if edited == string(stored) {
		t.Fatalf("could not add an execution setting to:\n%s", stored)
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := workgraph.AssociateWithLLM(workgraph.LLMAssociateConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client}); err == nil || !strings.Contains(err.Error(), "system-one profiles run direct only") {
		t.Fatalf("expected a bridge-configured system-one profile to be refused, got %v", err)
	}
	if calls := len(fake.received()); calls != 0 {
		t.Fatalf("a refused profile called the model %d times", calls)
	}
}

func TestScheduledAssociateRunsSystemOneProfilesDirectly(t *testing.T) {
	home := initSystemOneHome(t, true)
	fake := newSystemOneFake(t)
	configureSystemOneProfile(t, home, "http://127.0.0.1:8000", "", "slack")
	units := insertAssociateThreads(t, home, time.Now().UTC().Add(-6*time.Hour), "C1", systemOneContainer, 1)
	fake.answer("thread 0 in C1", systemOneChoice("p2", 0.9, map[string]any{"p2": 0.7, "no_project": 0.3}), systemOneChoice("build", 1, map[string]any{"build": 1}))

	scheduled, err := workgraph.ScheduleLLMAssociate(workgraph.LLMAssociateScheduleConfig{HomeDir: home.HomeDir, MemoryDir: home.MemoryDir, HTTPClient: fake.client})
	if err != nil || !scheduled.Created || !scheduled.Executed {
		t.Fatalf("expected the daemon to emit and execute the request directly, got %+v %v", scheduled, err)
	}
	if calls := len(fake.received()); calls != 1 {
		t.Fatalf("expected one System One request, got %d", calls)
	}
	link := currentProjectLink(t, workgraph.ProjectLinkStoreConfig{HomeDir: home.HomeDir}, units[0].ID)
	if link.Project != "harbor-harness" || link.Confidence != 0.7 || link.State != "proposed" {
		t.Fatalf("expected a proposed link at the returned probability, got %+v", link)
	}
}

func initSystemOneHome(t *testing.T, withCatalog bool) associateHome {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	home, memory := filepath.Join(root, "state"), filepath.Join(root, "memory")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: home, MemoryDir: memory})
	if err != nil {
		t.Fatal(err)
	}
	if withCatalog {
		writeProjectMemory(t, memory, "atlas-bot", "# Atlas Bot\n")
		writeProjectMemory(t, memory, "harbor-harness", "---\nworkstream: harbor\n---\n# Harbor Harness\n\nTest harness.\n")
		writeProjectMemory(t, memory, "orbit-segments", "---\nworkstream: harbor\n---\n# Orbit Segments\n")
	}
	return associateHome{HomeDir: home, MemoryDir: memory, DatabasePath: initialized.DatabasePath}
}

func configureSystemOneProfile(t *testing.T, home associateHome, baseURL, apiKeyEnv string, sources ...string) {
	t.Helper()
	if _, err := workgraph.AddLLMProfile(workgraph.LLMAddProfileConfig{HomeDir: home.HomeDir, Name: "decisions", Provider: "system-one", BaseURL: baseURL, Model: "clef-flash", APIKeyEnv: apiKeyEnv}); err != nil {
		t.Fatal(err)
	}
	configureSystemOneRoute(t, home, sources...)
}

func configureSystemOneRoute(t *testing.T, home associateHome, sources ...string) {
	t.Helper()
	if _, err := workgraph.UseLLMProfile(workgraph.LLMUseProfileConfig{HomeDir: home.HomeDir, Name: "decisions", Task: "associate"}); err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.ConfigureLLMSources(workgraph.LLMSourcesConfig{HomeDir: home.HomeDir, Task: "associate", Enable: sources}); err != nil {
		t.Fatal(err)
	}
}

func writeSystemOneManagedSettings(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertNoSystemOneSecrets(t *testing.T, text string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(text, secret) {
			t.Fatalf("diagnostic disclosed %q:\n%q", secret, text)
		}
	}
}

type systemOneQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type systemOneRequestBody struct {
	Model     string                       `json:"model"`
	State     string                       `json:"state"`
	Questions map[string]systemOneQuestion `json:"questions"`
}

type systemOneReceived struct {
	method        string
	url           string
	authorization string
	contentType   string
	raw           string
	body          systemOneRequestBody
}

type systemOneFake struct {
	t        *testing.T
	client   *http.Client
	envelope bool
	redirect string
	failure  func() (*http.Response, error)
	mu       sync.Mutex
	requests []systemOneReceived
	answers  []systemOneScripted
}

type systemOneScripted struct {
	stateContains string
	answers       map[string]any
}

func newSystemOneFake(t *testing.T) *systemOneFake {
	t.Helper()
	fake := &systemOneFake{t: t}
	fake.client = &http.Client{Transport: associateRoundTripper(fake.roundTrip)}
	return fake
}

func (fake *systemOneFake) roundTrip(request *http.Request) (*http.Response, error) {
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	var body systemOneRequestBody
	if err := json.Unmarshal(raw, &body); err != nil {
		fake.t.Errorf("System One request is not the documented JSON shape: %v\n%s", err, raw)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.requests = append(fake.requests, systemOneReceived{method: request.Method, url: request.URL.String(), authorization: request.Header.Get("Authorization"), contentType: request.Header.Get("Content-Type"), raw: string(raw), body: body})
	if fake.failure != nil {
		return fake.failure()
	}
	recorder := httptest.NewRecorder()
	if fake.redirect != "" && request.URL.String() != fake.redirect {
		recorder.Header().Set("Location", fake.redirect)
		recorder.WriteHeader(http.StatusTemporaryRedirect)
		return recorder.Result(), nil
	}
	for _, scripted := range fake.answers {
		if strings.Contains(body.State, scripted.stateContains) {
			response := map[string]any{"model": body.Model, "answers": scripted.answers, "usage": map[string]int{"input_tokens": len(raw), "output_tokens": 0}}
			if fake.envelope {
				response = map[string]any{"result": response, "success": true}
			}
			_ = json.NewEncoder(recorder).Encode(response)
			return recorder.Result(), nil
		}
	}
	recorder.WriteHeader(http.StatusBadRequest)
	_ = json.NewEncoder(recorder).Encode(map[string]string{"error": "unscripted state"})
	return recorder.Result(), nil
}

func (fake *systemOneFake) answer(stateContains string, project, kind map[string]any) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.answers = append(fake.answers, systemOneScripted{stateContains: stateContains, answers: map[string]any{"project": project, "kind": kind}})
}

func (fake *systemOneFake) received() []systemOneReceived {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	received := fake.requests
	fake.requests = nil
	return received
}

func systemOneChoice(choice string, confidence float64, probabilities map[string]any) map[string]any {
	return map[string]any{"type": "choice", "choice": choice, "confidence": confidence, "probabilities": probabilities}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
