package workgraph

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const projectActivityAutoHint = "workgraph projects review --auto"

type projectActivity struct {
	catalog        ProjectCatalog
	mappings       ContainerMappings
	eventUnits     map[string]string
	unitContainers map[string]string
	links          map[string]ProjectLink
	groups         []*projectActivityGroup
}

type projectActivityGroup struct {
	name      string
	label     string
	events    int
	prCount   int
	agentPRs  []string
	kinds     map[string]bool
	autoUnits map[string]bool
	children  []*projectActivityGroup
}

type agentPullRequest struct {
	UnitID    string
	Container string
	Line      string
}

func loadProjectActivity(db *sql.DB, memoryDir string, now time.Time, gitEmails []string) (*projectActivity, error) {
	catalog, err := LoadProjectCatalog(memoryDir)
	if err != nil {
		return nil, err
	}
	mappings, err := LoadContainerMappings(db)
	if err != nil {
		return nil, fmt.Errorf("load container mappings: %w", err)
	}
	units, err := projectUnits(db, now, now.Location(), normalizedEmailSet(gitEmails))
	if err != nil {
		return nil, err
	}
	activity := &projectActivity{catalog: catalog, mappings: mappings, eventUnits: map[string]string{}, unitContainers: map[string]string{}, links: map[string]ProjectLink{}}
	for _, unit := range units {
		activity.unitContainers[unit.ID] = unit.Container
		for _, id := range unit.EventIDs {
			activity.eventUnits[id] = unit.ID
		}
	}
	present, err := projectLinksTablePresent(db)
	if err != nil || !present {
		return activity, err
	}
	links, err := queryProjectLinks(db, `WHERE state = 'applied' ORDER BY id`)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		activity.links[link.UnitID] = link
	}
	return activity, nil
}

func (activity *projectActivity) linkedToProject(eventID string) bool {
	link, ok := activity.links[activity.eventUnits[eventID]]
	return ok && link.Target == "project"
}

func (activity *projectActivity) addEvent(eventID, container string) {
	unitID := activity.eventUnits[eventID]
	path, link := activity.placement(unitID, container)
	for _, group := range path {
		group.events++
	}
	if link == nil || len(path) == 0 {
		return
	}
	leaf := path[len(path)-1]
	if link.Kind != "" {
		leaf.kinds[link.Kind] = true
	}
	if link.Auto {
		leaf.autoUnits[unitID] = true
	}
}

func (activity *projectActivity) addAgentPullRequest(pr agentPullRequest) {
	container := pr.Container
	if known := activity.unitContainers[pr.UnitID]; known != "" {
		container = known
	}
	path, _ := activity.placement(pr.UnitID, container)
	if len(path) == 0 {
		return
	}
	for _, group := range path {
		group.prCount++
	}
	leaf := path[len(path)-1]
	leaf.agentPRs = append(leaf.agentPRs, pr.Line)
}

func (activity *projectActivity) placement(unitID, container string) ([]*projectActivityGroup, *ProjectLink) {
	link, linked := activity.links[unitID]
	if linked && link.Target == "project" {
		return activity.path(activity.catalog.workstreamOf(link.Project), link.Project, "project"), &link
	}
	if !linked {
		if project := activity.mappedProject(container); project != "" {
			return activity.path(activity.catalog.workstreamOf(project), project, "project"), nil
		}
	}
	if container == "" {
		return nil, nil
	}
	workstream := ""
	if workstreams := activity.mappings.Workstreams[container]; len(workstreams) == 1 {
		workstream = workstreams[0]
	}
	return activity.path(workstream, container, ""), nil
}

func (activity *projectActivity) mappedProject(container string) string {
	projects := activity.mappings.Projects[container]
	if len(projects) != 1 || !catalogHasProject(activity.catalog, projects[0]) {
		return ""
	}
	return projects[0]
}

func (activity *projectActivity) path(workstream, name, label string) []*projectActivityGroup {
	if workstream == "" {
		return []*projectActivityGroup{findProjectActivityGroup(&activity.groups, name, label)}
	}
	parent := findProjectActivityGroup(&activity.groups, workstream, "workstream")
	return []*projectActivityGroup{parent, findProjectActivityGroup(&parent.children, name, label)}
}

func findProjectActivityGroup(groups *[]*projectActivityGroup, name, label string) *projectActivityGroup {
	for _, group := range *groups {
		if group.name == name && group.label == label {
			return group
		}
	}
	group := &projectActivityGroup{name: name, label: label, kinds: map[string]bool{}, autoUnits: map[string]bool{}}
	*groups = append(*groups, group)
	return group
}

func (activity *projectActivity) lines() []string {
	var lines []string
	for _, group := range sortedProjectActivityGroups(activity.groups) {
		lines = append(lines, group.render("")...)
		for _, child := range sortedProjectActivityGroups(group.children) {
			lines = append(lines, child.render("  ")...)
		}
	}
	return lines
}

func (group *projectActivityGroup) render(indent string) []string {
	name := group.name
	if group.label != "" {
		name += " " + group.label
	}
	var counts []string
	if group.events > 0 {
		counts = append(counts, pluralize(group.events, "event"))
	}
	if group.prCount > 0 {
		counts = append(counts, pluralize(group.prCount, "agent PR"))
	}
	line := indent + "- " + name + ": " + strings.Join(counts, ", ")
	if kinds := sortedKeys(group.kinds); len(kinds) > 0 {
		line += "; " + strings.Join(kinds, ", ")
	}
	if len(group.autoUnits) > 0 {
		line += fmt.Sprintf("; %d auto-applied (%s)", len(group.autoUnits), projectActivityAutoHint)
	}
	lines := []string{line}
	for _, pr := range group.agentPRs {
		lines = append(lines, indent+"  - "+pr)
	}
	return lines
}

func sortedProjectActivityGroups(groups []*projectActivityGroup) []*projectActivityGroup {
	sorted := append([]*projectActivityGroup{}, groups...)
	sort.SliceStable(sorted, func(i, j int) bool {
		left, right := sorted[i].events+sorted[i].prCount, sorted[j].events+sorted[j].prCount
		if left != right {
			return left > right
		}
		return sorted[i].name < sorted[j].name
	})
	return sorted
}

func agentPullRequestsSince(db *sql.DB, since, now time.Time) ([]agentPullRequest, error) {
	var exists int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'github_pr_observations'`).Scan(&exists); err != nil || exists == 0 {
		return nil, err
	}
	rows, err := db.Query(`SELECT observation_json FROM github_pr_observations ORDER BY node_id, role`)
	if err != nil {
		return nil, fmt.Errorf("query pull request observations: %w", err)
	}
	defer rows.Close()
	var observations []githubPRObservation
	onBehalf := map[string]bool{}
	for rows.Next() {
		var raw string
		var observation githubPRObservation
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &observation) != nil || observation.AuthorKind != "bot" {
			continue
		}
		if observation.Role == githubPRRoleAuthor {
			onBehalf[observation.NodeID] = true
		}
		observations = append(observations, observation)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var prs []agentPullRequest
	for _, observation := range observations {
		created := parseNextTime(observation.CreatedAt)
		if seen[observation.NodeID] || !created.After(since) || created.After(now) {
			continue
		}
		seen[observation.NodeID] = true
		author := nextDisplayText(observation.AuthorLogin)
		if onBehalf[observation.NodeID] {
			author += ", on your behalf"
		}
		prs = append(prs, agentPullRequest{
			UnitID:    fmt.Sprintf("github.pr:%s#%d", strings.ToLower(observation.Repository), observation.Number),
			Container: githubProjectName(observation.Repository),
			Line:      fmt.Sprintf("agent PR %s#%d %s (%s)", nextDisplayText(observation.Repository), observation.Number, nextDisplayText(observation.Title), author),
		})
	}
	return prs, nil
}
