package workgraph

import (
	"database/sql"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
	"unicode"
)

type MemoryDoctorConfig struct {
	HomeDir      string
	DatabasePath string
	MemoryDir    string
	Project      string
}

type MemoryProjectCandidate struct {
	Project    string
	Title      string
	Source     string
	Reason     string
	Workstream string
	EventCount int
	Latest     time.Time
	score      int
}

type MemoryDoctorResult struct {
	Candidates []MemoryProjectCandidate
	Message    string
}

func DoctorMemory(config MemoryDoctorConfig) (MemoryDoctorResult, error) {
	path, err := resumeDatabasePath(ResumeConfig{HomeDir: config.HomeDir, DatabasePath: config.DatabasePath})
	if err != nil {
		return MemoryDoctorResult{}, err
	}
	db, err := sql.Open("sqlite3", path)
	if err != nil {
		return MemoryDoctorResult{}, err
	}
	defer db.Close()
	events, err := loadResumeEvents(db, time.Local)
	if err != nil {
		return MemoryDoctorResult{}, err
	}
	return inspectMemoryProjects(db, events, config)
}

func inspectMemoryProjects(db *sql.DB, events []ResumeEvent, config MemoryDoctorConfig) (MemoryDoctorResult, error) {
	memoryDir, err := resolveMemoryDir(config.MemoryDir)
	if err != nil {
		return MemoryDoctorResult{}, err
	}
	projects := []string{}
	if config.Project != "" {
		projects = append(projects, config.Project)
	} else {
		entries, err := os.ReadDir(projectMemoryDir(memoryDir))
		if err != nil && !os.IsNotExist(err) {
			return MemoryDoctorResult{}, err
		}
		for _, entry := range entries {
			if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".md") {
				projects = append(projects, strings.TrimSuffix(entry.Name(), ".md"))
			}
		}
	}
	catalog, err := LoadProjectCatalog(memoryDir)
	if err != nil {
		return MemoryDoctorResult{}, err
	}
	mappings, err := LoadContainerMappings(db)
	if err != nil {
		return MemoryDoctorResult{}, err
	}
	counts, latest := memoryContainerActivity(events)
	var sources []string
	for source := range counts {
		sources = append(sources, source)
	}
	sort.Slice(sources, func(i, j int) bool {
		if counts[sources[i]] != counts[sources[j]] {
			return counts[sources[i]] > counts[sources[j]]
		}
		return sources[i] < sources[j]
	})
	result := MemoryDoctorResult{}
	lines := []string{"Memory project diagnostics"}
	lines = append(lines, frontmatterProblemLines(catalog, config.Project)...)
	for _, project := range projects {
		doc, path, err := loadProjectMemory(memoryDir, project)
		if err != nil {
			return MemoryDoctorResult{}, err
		}
		if doc == nil {
			lines = append(lines, fmt.Sprintf("Memory project %q has no document; initialize it before linking sources.", project))
			continue
		}
		title := memoryProjectTitle(projectMemoryBody(doc.Content), project)
		mapped, err := memoryProjectSources(db, path)
		if err != nil {
			return MemoryDoctorResult{}, err
		}
		linked := map[string]bool{project: true}
		lines = append(lines, "", fmt.Sprintf("Project: %s (%s)", title, path))
		for _, source := range mapped {
			linked[source] = true
			lines = append(lines, fmt.Sprintf("Linked: %s (%d events)", source, counts[source]))
		}
		for source := range mappings.Workstreams {
			linked[source] = true
		}
		for source := range mappings.Projects {
			linked[source] = true
		}
		var candidates []MemoryProjectCandidate
		for _, source := range sources {
			if linked[source] {
				continue
			}
			score, reason := memoryNameSimilarity(title, source)
			if other, why := memoryNameSimilarity(project, source); other > score {
				score, reason = other, why
			}
			if score == 0 {
				continue
			}
			candidate := MemoryProjectCandidate{Project: project, Title: title, Source: source, Reason: reason, EventCount: counts[source], Latest: latest[source], score: score}
			if acronym, ok := strings.CutPrefix(reason, sharedAcronymReasonPrefix); ok {
				candidate.Workstream = catalog.workstreamOf(projectSlug(project))
				if candidate.Workstream == "" {
					candidate.Workstream = strings.ToLower(acronym)
				}
			}
			candidates = append(candidates, candidate)
		}
		sort.Slice(candidates, func(i, j int) bool {
			if candidates[i].score != candidates[j].score {
				return candidates[i].score > candidates[j].score
			}
			if candidates[i].EventCount != candidates[j].EventCount {
				return candidates[i].EventCount > candidates[j].EventCount
			}
			return candidates[i].Source < candidates[j].Source
		})
		if len(candidates) > 5 {
			candidates = candidates[:5]
		}
		result.Candidates = append(result.Candidates, candidates...)
		for _, candidate := range candidates {
			lines = append(lines, memoryCandidateMessage(config, candidate))
		}
		if len(candidates) == 0 && len(mapped) == 0 && counts[project] == 0 {
			lines = append(lines, "No exact mapping or reliable name match. Select a container manually with memory link --source <container> <memory-project>.")
		}
	}
	if len(projects) == 0 {
		lines = append(lines, "No project memory documents found.")
	}
	if len(result.Candidates) == 0 {
		lines = append(lines, "", "Containers available for manual linking:")
		for i, source := range sources {
			if i >= 5 {
				break
			}
			lines = append(lines, fmt.Sprintf("- %s (%d events)", source, counts[source]))
		}
		if len(sources) == 0 {
			lines = append(lines, "No captured container activity found. Check workgraph status.")
		}
		if len(sources) > 5 {
			lines = append(lines, "Use workgraph resume --all to inspect additional containers.")
		}
	}
	lines = append(lines, "", "No mappings or memory files are changed by this report. Use memory doctor --interactive to review candidates.")
	result.Message = strings.Join(lines, "\n")
	return result, nil
}

func memoryProjectTitle(content, fallback string) string {
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "# ") {
			if title := markdownPlainText(strings.TrimPrefix(line, "# ")); title != "" {
				return title
			}
		}
	}
	return fallback
}

func memoryNameWords(name string) []string {
	runes := []rune(name)
	var split strings.Builder
	for i, r := range runes {
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(runes[i-1]) || i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1])) {
			split.WriteByte(' ')
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			split.WriteRune(unicode.ToLower(r))
		} else {
			split.WriteByte(' ')
		}
	}
	return strings.Fields(split.String())
}

func memoryNameSimilarity(name, source string) (int, string) {
	a, b := memoryNameWords(name), memoryNameWords(source)
	if len(a) == 0 || len(b) == 0 {
		return 0, ""
	}
	if strings.Join(a, "") == strings.Join(b, "") {
		return 3, "normalized names match"
	}
	acronym := func(words []string) string {
		if len(words) < 3 {
			return ""
		}
		var out strings.Builder
		for _, word := range words {
			for _, r := range word {
				out.WriteRune(r)
				break
			}
		}
		return out.String()
	}
	for _, pair := range []struct {
		words   []string
		acronym string
	}{{a, acronym(b)}, {b, acronym(a)}} {
		if pair.acronym == "" {
			continue
		}
		for _, word := range pair.words {
			if word == pair.acronym {
				return 2, sharedAcronymReasonPrefix + strings.ToUpper(word)
			}
		}
	}
	for _, x := range a {
		if len([]rune(x)) < 3 || associationGenericProjects[x] || associationStopTokens[x] || x == "api" || x == "app" || x == "service" || x == "memory" || x == "team" {
			continue
		}
		for _, y := range b {
			if x == y {
				return 1, "shared name token " + x
			}
		}
	}
	return 0, ""
}

func memoryContainerActivity(events []ResumeEvent) (map[string]int, map[string]time.Time) {
	counts := map[string]int{}
	latest := map[string]time.Time{}
	for _, event := range events {
		if event.Project == "" || isTransientResumePath(event.Path) {
			continue
		}
		counts[event.Project]++
		if event.Timestamp.After(latest[event.Project]) {
			latest[event.Project] = event.Timestamp
		}
	}
	return counts, latest
}

func memoryCandidateMessage(config MemoryDoctorConfig, candidate MemoryProjectCandidate) string {
	link := append(memoryLinkCommand(config), "--source", memoryShellQuote(candidate.Source), memoryShellQuote(candidate.Project))
	message := fmt.Sprintf("Possible mapping: %s → %s (%s; %d events; latest %s)\n  To link: %s", candidate.Title, candidate.Source, candidate.Reason, candidate.EventCount, candidate.Latest.Format(time.RFC3339), strings.Join(link, " "))
	if candidate.Workstream == "" {
		return message
	}
	workstream := append(memoryLinkCommand(config), "--workstream", memoryShellQuote(candidate.Workstream), "--source", memoryShellQuote(candidate.Source))
	return message + fmt.Sprintf("\n  Workstream suggestion: %s (a shared acronym often names a workstream rather than one project)\n  To map the container to the workstream: %s", candidate.Workstream, strings.Join(workstream, " "))
}

func memoryLinkCommand(config MemoryDoctorConfig) []string {
	args := []string{"workgraph", "memory", "link"}
	for _, option := range []struct{ name, value string }{{"--home", config.HomeDir}, {"--memory", config.MemoryDir}, {"--database", config.DatabasePath}} {
		if option.value != "" {
			args = append(args, option.name, memoryShellQuote(option.value))
		}
	}
	return args
}

func frontmatterProblemLines(catalog ProjectCatalog, project string) []string {
	var lines []string
	for _, problem := range catalog.Problems {
		if project != "" && problem.Slug != projectSlug(project) {
			continue
		}
		lines = append(lines, "", fmt.Sprintf("Invalid frontmatter in %s: %s", problem.Path, problem.Problem))
	}
	if len(lines) > 0 {
		lines = append(lines, invalidFrontmatterDoctorNote)
	}
	return lines
}

func memoryShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func memoryDiscoveryHints(db *sql.DB, events []ResumeEvent, config MemoryDoctorConfig) string {
	result, err := inspectMemoryProjects(db, events, config)
	if err != nil {
		return "\n\nMemory diagnostics unavailable: " + err.Error()
	}
	if len(result.Candidates) == 0 {
		return ""
	}
	lines := []string{"", "", "Possible memory project mappings (review before linking)"}
	for i, candidate := range result.Candidates {
		if i >= 5 {
			break
		}
		lines = append(lines, memoryCandidateMessage(config, candidate))
	}
	lines = append(lines, "Use workgraph memory doctor --interactive to review candidates.")
	return strings.Join(lines, "\n")
}
