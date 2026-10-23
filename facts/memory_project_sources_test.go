package facts

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	workgraph "github.com/jystringfellow/workgraph"
)

func TestMemoryProjectSourcesFlowThroughEvidence(t *testing.T) {
	root := t.TempDir()
	home, memory := filepath.Join(root, "state"), filepath.Join(root, "memory")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: home, MemoryDir: memory})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := workgraph.InitProjectMemory(workgraph.ProjectMemoryInitConfig{HomeDir: home, MemoryDir: memory, Project: "MPO Harness"})
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(doc.Path)
	if err != nil {
		t.Fatal(err)
	}
	for i, project := range []string{"Mercury.Platform.Orchestration", "widgets-ui", "unrelated"} {
		insertEvent(t, initialized.DatabasePath, storedEvent{ID: project, Type: "file.modified", Timestamp: time.Now().Add(time.Duration(i) * time.Second), Project: project, Payload: `{"path":"/tmp/work/notes.md"}`, Summary: project})
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`DROP TABLE memory_project_sources`); err != nil {
		t.Fatal(err)
	}
	before, err := workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{HomeDir: home, MemoryDir: memory, Project: "MPO Harness"})
	if err != nil || len(before.Suggestions) != 0 {
		t.Fatalf("legacy database read: %+v %v", before, err)
	}
	var tableCount int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'memory_project_sources'`).Scan(&tableCount); err != nil || tableCount != 0 {
		t.Fatal("read query migrated legacy database")
	}
	args := []string{"--home", home, "--memory", memory}
	linkArgs := append([]string{"memory", "link"}, args...)
	linkArgs = append(linkArgs, "--source", "Mercury.Platform.Orchestration", "--source", "widgets-ui", "MPO Harness")
	for i := 0; i < 2; i++ {
		output, err := runworkgraph(t, repoRoot(t), linkArgs...)
		if err != nil {
			t.Fatalf("link: %v %s", err, output)
		}
	}
	contents, err := os.ReadFile(doc.Path)
	if err != nil || string(contents) != string(original) {
		t.Fatal("link rewrote authored memory")
	}
	suggestions, err := workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{HomeDir: home, MemoryDir: memory, Project: "MPO Harness"})
	if err != nil || len(suggestions.Suggestions) != 2 {
		t.Fatalf("mapped suggestions: %+v %v", suggestions, err)
	}
	resumed, err := workgraph.Resume(workgraph.ResumeConfig{HomeDir: home, MemoryDir: memory, Project: "MPO Harness"})
	if err != nil || len(resumed.Events) != 2 || resumed.Memory == nil {
		t.Fatalf("mapped resume: %+v %v", resumed, err)
	}
	for _, event := range resumed.Events {
		if event.Project == "MPO Harness" {
			t.Fatal("rewrote captured identity")
		}
	}
	links, err := workgraph.ListMemoryLinks(workgraph.MemoryLinksConfig{HomeDir: home, MemoryDir: memory, Project: "MPO Harness"})
	if err != nil || len(links.Links) != 0 || !strings.Contains(links.Message, "Container mappings\n- Mercury.Platform.Orchestration\n- widgets-ui") {
		t.Fatalf("mapping visibility: %+v %v", links, err)
	}
	_, err = workgraph.PromoteMemory(workgraph.MemoryPromoteConfig{HomeDir: home, MemoryDir: memory, Project: "MPO Harness", EvidenceID: "widgets-ui", Text: "UI supports this workstream."})
	if err != nil {
		t.Fatal(err)
	}
	unlink := append([]string{"memory", "unlink"}, args...)
	unlink = append(unlink, "--source", "widgets-ui", "MPO Harness")
	output, err := runworkgraph(t, repoRoot(t), unlink...)
	if err != nil {
		t.Fatalf("unlink: %v %s", err, output)
	}
	suggestions, err = workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{HomeDir: home, MemoryDir: memory, Project: "MPO Harness"})
	if err != nil || len(suggestions.Suggestions) != 1 {
		t.Fatalf("unlink not reflected: %+v %v", suggestions, err)
	}
	links, err = workgraph.ListMemoryLinks(workgraph.MemoryLinksConfig{HomeDir: home, MemoryDir: memory, Project: "MPO Harness"})
	if err != nil || len(links.Links) != 1 {
		t.Fatalf("unlink lost promoted evidence: %+v %v", links, err)
	}
	bad := append([]string{"memory", "link"}, args...)
	bad = append(bad, "--source", "unrelated", "--source", "does-not-exist", "MPO Harness")
	if output, err := runworkgraph(t, repoRoot(t), bad...); err == nil {
		t.Fatalf("accepted unknown source: %s", output)
	}
	suggestions, err = workgraph.SuggestMemoryUpdates(workgraph.MemorySuggestConfig{HomeDir: home, MemoryDir: memory, Project: "MPO Harness"})
	if err != nil || len(suggestions.Suggestions) != 1 {
		t.Fatal("partially applied invalid source list")
	}
}

func TestStoreBackfillsEmptySlackContainersFromChannelName(t *testing.T) {
	root := t.TempDir()
	home, memory := filepath.Join(root, "state"), filepath.Join(root, "memory")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: home, MemoryDir: memory})
	if err != nil {
		t.Fatal(err)
	}
	payloads := map[string]string{
		"empty":    `{"channel_id":"C0DEMO001","channel_name":"squad-demo-excellence","conversation_type":"channel","ts":"1789300800.000100","text":"Empty project"}`,
		"null":     `{"channel_id":"C0DEMO002","channel_name":"#guild-demo","ts":"1789300801.000100","text":"Null project"}`,
		"explicit": `{"channel_id":"C0DEMO003","channel_name":"demo-changes","ts":"1789300802.000100","text":"Explicit project"}`,
		"dm":       `{"channel_id":"D0DEMO004","channel_name":"DM: Lila","conversation_type":"im","ts":"1789300803.000100","text":"Direct message"}`,
		"raw":      `{"channel_id":"C0DEMO005","channel_name":"C0DEMO005","ts":"1789300804.000100","text":"Raw id name"}`,
		"unnamed":  `{"channel_id":"C0DEMO006","ts":"1789300805.000100","text":"No name"}`,
		"file":     `{"path":"/tmp/work/notes.md","channel_name":"not-slack"}`,
		"alias":    `{"channel_id":"C0DEMO001","ts":"1789300806.000100","text":"Alias only"}`,
		"hash":     `{"channel_id":"C0DEMO007","ts":"1789300807.000100","text":"Hash container"}`,
		"reply":    `{"channel_id":"C0DEMO001","channel_name":"squad-demo-excellence","ts":"1789300808.000200","thread_ts":"1789300800.000100","text":"Reply"}`,
		"legacy":   `{"channel_id":"C0DEMO008","channel_name":"private-name","ts":"1789300809.000100","text":"Untyped legacy"}`,
		"leak":     `{"channel_id":"C0DEMO008","conversation_type":"channel","ts":"1789300810.000100","text":"Typed without name"}`,
	}
	now := time.Now()
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "empty", Source: "slack", Type: "slack.message", Timestamp: now, Payload: payloads["empty"]})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "explicit", Source: "slack", Type: "slack.message", Timestamp: now, Project: "demo-production", Payload: payloads["explicit"]})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "dm", Source: "slack", Type: "slack.message", Timestamp: now, Payload: payloads["dm"]})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "raw", Source: "slack", Type: "slack.message", Timestamp: now, Payload: payloads["raw"]})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "unnamed", Source: "slack", Type: "slack.message", Timestamp: now, Payload: payloads["unnamed"]})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "file", Type: "file.modified", Timestamp: now, Payload: payloads["file"]})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "alias", Source: "slack", Type: "slack.message", Timestamp: now, Payload: payloads["alias"]})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "hash", Source: "slack", Type: "slack.message", Timestamp: now, Project: "#team-demo", Payload: payloads["hash"]})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "reply", Source: "slack", Type: "slack.reply", Timestamp: now, Payload: payloads["reply"]})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "legacy", Source: "slack", Type: "slack.message", Timestamp: now, Project: "kept", Payload: payloads["legacy"]})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "leak", Source: "slack", Type: "slack.message", Timestamp: now, Payload: payloads["leak"]})
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO events (id, source, type, timestamp, payload_json, project, created_at) VALUES ('null', 'slack', 'slack.message', ?, ?, NULL, ?)`, now.UTC().Format(time.RFC3339Nano), payloads["null"], now.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}

	expected := map[string]struct{ eventType, project string }{
		"empty":    {"slack.message", "squad-demo-excellence"},
		"null":     {"slack.message", "guild-demo"},
		"explicit": {"slack.message", "demo-production"},
		"dm":       {"slack.message", "DM: Lila"},
		"raw":      {"slack.message", ""},
		"unnamed":  {"slack.message", ""},
		"file":     {"file.modified", ""},
		"alias":    {"slack.message", "squad-demo-excellence"},
		"hash":     {"slack.message", "team-demo"},
		"reply":    {"slack.thread_reply", "squad-demo-excellence"},
		"legacy":   {"slack.message", "kept"},
		"leak":     {"slack.message", ""},
	}
	var logs strings.Builder
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	for open := 1; open <= 2; open++ {
		logs.Reset()
		if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: home, MemoryDir: memory}); err != nil {
			t.Fatal(err)
		}
		repaired := strings.Contains(logs.String(), "backfilled slack")
		if open == 1 && !repaired {
			t.Fatalf("expected the first open to report the Slack repair, got %q", logs.String())
		}
		if open == 2 && repaired {
			t.Fatalf("expected a repaired store to need no work on reopen, got %q", logs.String())
		}
		for id, want := range expected {
			var eventType, payloadJSON string
			var project sql.NullString
			if err := db.QueryRow(`SELECT type, project, payload_json FROM events WHERE id = ?`, id).Scan(&eventType, &project, &payloadJSON); err != nil {
				t.Fatal(err)
			}
			if eventType != want.eventType || project.String != want.project {
				t.Fatalf("open %d: expected %s as %s in %q, got %s in %+v", open, id, want.eventType, want.project, eventType, project)
			}
			if payloadJSON != payloads[id] {
				t.Fatalf("open %d: backfill rewrote %s payload: %s", open, id, payloadJSON)
			}
		}
	}
}

func TestStoreGivesEachSlackDMConversationItsOwnContainer(t *testing.T) {
	root := t.TempDir()
	home, memory := filepath.Join(root, "state"), filepath.Join(root, "memory")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: home, MemoryDir: memory})
	if err != nil {
		t.Fatal(err)
	}
	type row struct{ project, payload, want string }
	rows := map[string]row{
		"generic-dm":        {"DM", `{"channel_id":"D0DEMO010","channel_type":"im","ts":"1789300800.000100","text":"Generic DM"}`, "DM (D0DEMO010)"},
		"generic-group-a":   {"Group DM", `{"channel_id":"C0DEMO011","channel_name":"Group DM","ts":"1789300801.000100","text":"First group"}`, "Group DM (C0DEMO011)"},
		"generic-group-b":   {"Group DM", `{"channel_id":"C0DEMO012","channel_name":"Group DM","conversation_type":"mpim","ts":"1789300802.000100","text":"Second group"}`, "Group DM: Lila, Omar"},
		"named-group-b":     {"Group DM: Lila, Omar", `{"channel_id":"C0DEMO012","channel_name":"Group DM: Lila, Omar","conversation_type":"mpim","ts":"1789300803.000100","text":"Named group"}`, "Group DM: Lila, Omar"},
		"legacy-paren":      {"DM (Lila Park, Omar Diaz)", `{"channel_id":"D0DEMO013","ts":"1789300804.000100","text":"Legacy DM"}`, "DM: Lila Park, Omar Diaz"},
		"legacy-with":       {"Group DM with Lila, Omar, Ren", `{"channel_id":"C0DEMO014","channel_type":"group_dm","ts":"1789300805.000100","text":"Legacy group"}`, "Group DM: Lila, Omar, Ren"},
		"legacy-colon":      {"dm:Bonusly", `{"channel_id":"D0DEMO015","ts":"1789300806.000100","text":"Bot DM"}`, "DM: Bonusly"},
		"mpdm":              {"mpdm-lila--omar--ren-1", `{"channel_id":"G0DEMO016","conversation_type":"mpim","ts":"1789300807.000100","text":"Slack mpdm name"}`, "Group DM: lila, omar, ren"},
		"empty-dm":          {"", `{"channel_id":"D0DEMO017","channel_type":"dm","ts":"1789300808.000100","text":"Unnamed DM"}`, "DM (D0DEMO017)"},
		"empty-generic":     {"", `{"channel_id":"C0DEMO018","channel_name":"Group DM","ts":"1789300809.000100","text":"Generic payload name"}`, "Group DM (C0DEMO018)"},
		"generic-no-id":     {"DM", `{"ts":"1789300810.000100","text":"No conversation id"}`, "DM"},
		"dm-named-channel":  {"dm-requests", `{"channel_id":"C0DEMO019","channel_name":"dm-requests","conversation_type":"channel","ts":"1789300811.000100","text":"Real channel"}`, "dm-requests"},
		"canonical":         {"DM: Lila", `{"channel_id":"D0DEMO020","channel_name":"DM: Lila","conversation_type":"im","ts":"1789300812.000100","text":"Canonical"}`, "DM: Lila"},
		"upper-group":       {"GROUP DM", `{"channel_id":"C0DEMO021","ts":"1789300813.000100","text":"Uppercase generic group"}`, "Group DM (C0DEMO021)"},
		"upper-im":          {"IM", `{"channel_id":"D0DEMO022","ts":"1789300814.000100","text":"Uppercase im"}`, "DM (D0DEMO022)"},
		"upper-mpdm":        {"MPDM-ada--grace-1", `{"channel_id":"G0DEMO023","ts":"1789300815.000100","text":"Uppercase mpdm"}`, "Group DM: ada, grace"},
		"raw-dm-id":         {"D0DEMO024", `{"channel_id":"D0DEMO024","ts":"1789300816.000100","text":"Raw DM id"}`, "DM (D0DEMO024)"},
		"raw-channel-name":  {"C0DEMO030", `{"channel_id":"C0DEMO030","channel_name":"general","conversation_type":"channel","ts":"1789300822.000100","text":"Raw channel id with a name"}`, "C0DEMO030"},
		"raw-channel-alias": {"C0DEMO031", `{"channel_id":"C0DEMO031","conversation_type":"private_channel","ts":"1789300823.000100","text":"Raw channel id with an alias"}`, "C0DEMO031"},
		"channel-alias":     {"squad-alias", `{"channel_id":"C0DEMO031","channel_name":"squad-alias","conversation_type":"private_channel","ts":"1789300824.000100","text":"Channel alias"}`, "squad-alias"},
		"raw-untyped-label": {"C0DEMO032", `{"channel_id":"C0DEMO032","channel_name":"im","ts":"1789300825.000100","text":"Raw untyped id with a DM-looking name"}`, "C0DEMO032"},
		"raw-channel-id":    {"C0DEMO028", `{"channel_id":"C0DEMO028","conversation_type":"channel","ts":"1789300820.000100","text":"Raw channel id"}`, "C0DEMO028"},
		"empty-colon":       {"DM: ", `{"channel_id":"D0DEMO025","ts":"1789300817.000100","text":"Empty colon form"}`, "DM (D0DEMO025)"},
		"id-colon":          {"DM: D12345678", `{"channel_id":"D0DEMO026","ts":"1789300818.000100","text":"Id colon form"}`, "DM (D0DEMO026)"},
		"kind-over-label":   {"DM (Ada, Grace)", `{"channel_id":"C0DEMO027","conversation_type":"mpim","ts":"1789300819.000100","text":"Group labelled as DM"}`, "Group DM: Ada, Grace"},
		"kind-over-colon":   {"DM: Ada, Grace", `{"channel_id":"C0DEMO029","conversation_type":"mpim","ts":"1789300821.000100","text":"Canonical-looking group"}`, "Group DM: Ada, Grace"},
	}
	now := time.Now()
	for id, row := range rows {
		insertEvent(t, initialized.DatabasePath, storedEvent{ID: id, Source: "slack", Type: "slack.message", Timestamp: now, Project: row.project, Payload: row.payload})
	}
	db, err := sql.Open("sqlite3", initialized.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var logs strings.Builder
	log.SetOutput(&logs)
	defer log.SetOutput(os.Stderr)
	for open := 1; open <= 2; open++ {
		logs.Reset()
		if _, err := workgraph.Init(workgraph.InitConfig{HomeDir: home, MemoryDir: memory}); err != nil {
			t.Fatal(err)
		}
		repaired := strings.Contains(logs.String(), "backfilled slack")
		if open == 1 && !repaired {
			t.Fatalf("expected the first open to report the Slack repair, got %q", logs.String())
		}
		if open == 2 && repaired {
			t.Fatalf("expected a repaired store to need no work on reopen, got %q", logs.String())
		}
		for id, want := range rows {
			var project sql.NullString
			var payloadJSON string
			if err := db.QueryRow(`SELECT project, payload_json FROM events WHERE id = ?`, id).Scan(&project, &payloadJSON); err != nil {
				t.Fatal(err)
			}
			if project.String != want.want {
				t.Fatalf("open %d: expected %s container %q, got %q", open, id, want.want, project.String)
			}
			if payloadJSON != want.payload {
				t.Fatalf("open %d: backfill rewrote %s payload: %s", open, id, payloadJSON)
			}
		}
	}
}

func TestMemoryLinkAcceptsSlackContainerThatDoctorSuggests(t *testing.T) {
	root := t.TempDir()
	home, memory := filepath.Join(root, "state"), filepath.Join(root, "memory")
	initialized, err := workgraph.Init(workgraph.InitConfig{HomeDir: home, MemoryDir: memory})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := workgraph.InitProjectMemory(workgraph.ProjectMemoryInitConfig{HomeDir: home, MemoryDir: memory, Project: "Demo Excellence"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "named", Source: "slack", Type: "slack.message", Timestamp: now, Project: "#squad-demo-excellence", Payload: `{"channel_id":"C0DEMO001","channel_name":"squad-demo-excellence","ts":"1789300800.000100","text":"Named"}`})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "aliased", Source: "slack", Type: "slack.message", Timestamp: now.Add(time.Second), Payload: `{"channel_id":"C0DEMO002","ts":"1789300801.000100","text":"Alias only"}`})
	insertEvent(t, initialized.DatabasePath, storedEvent{ID: "alias-source", Source: "slack", Type: "slack.message", Timestamp: now.Add(2 * time.Second), Project: "C0DEMO002", Payload: `{"channel_id":"C0DEMO002","channel_name":"C0DEMO002","ts":"1789300802.000100","text":"Raw"}`})

	doctor, err := workgraph.DoctorMemory(workgraph.MemoryDoctorConfig{HomeDir: home, MemoryDir: memory, Project: "demo-excellence"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doctor.Message, "squad-demo-excellence") {
		t.Fatalf("doctor did not suggest the Slack container: %s", doctor.Message)
	}
	for _, candidate := range doctor.Candidates {
		if _, err := workgraph.UpdateMemoryProjectSources(workgraph.MemoryProjectSourcesConfig{HomeDir: home, MemoryDir: memory, Project: candidate.Project, Sources: []string{candidate.Source}}); err != nil {
			t.Fatalf("link rejected doctor candidate %q: %v", candidate.Source, err)
		}
	}
	if len(doctor.Candidates) == 0 {
		t.Fatalf("doctor offered no candidates: %s", doctor.Message)
	}
	if _, err := workgraph.UpdateMemoryProjectSources(workgraph.MemoryProjectSourcesConfig{HomeDir: home, MemoryDir: memory, Project: "demo-excellence", Sources: []string{"does-not-exist"}}); err == nil {
		t.Fatal("link accepted an unknown container")
	}
}
