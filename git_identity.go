package workgraph

import (
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const localGitIdentityWorkers = 8

type LocalGitIdentityConfig struct {
	HomeDir      string
	DatabasePath string
}

func LocalGitEmails(config LocalGitIdentityConfig) []string {
	emails := gitConfigEmails("config", "--global", "--get-all", "user.email")
	emails = append(emails, repositoryGitEmails(existingDirectories(capturedGitRepositories(config)))...)
	set := normalizedEmailSet(emails)
	normalized := make([]string, 0, len(set))
	for email := range set {
		normalized = append(normalized, email)
	}
	sort.Strings(normalized)
	return normalized
}

func repositoryGitEmails(repos []string) []string {
	jobs := make(chan string)
	results := make(chan []string)
	var workers sync.WaitGroup
	for range min(localGitIdentityWorkers, len(repos)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for repo := range jobs {
				results <- gitConfigEmails("-C", repo, "config", "--get-all", "user.email")
			}
		}()
	}
	go func() {
		for _, repo := range repos {
			jobs <- repo
		}
		close(jobs)
		workers.Wait()
		close(results)
	}()
	var emails []string
	for repoEmails := range results {
		emails = append(emails, repoEmails...)
	}
	return emails
}

func existingDirectories(paths []string) []string {
	var existing []string
	for _, path := range paths {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			existing = append(existing, path)
		}
	}
	return existing
}

func gitConfigEmails(args ...string) []string {
	output, err := exec.Command("git", args...).Output()
	if err != nil {
		return nil
	}
	return strings.Split(string(output), "\n")
}

func capturedGitRepositories(config LocalGitIdentityConfig) []string {
	homeDir, err := resolveHomeDir(config.HomeDir)
	if err != nil {
		return nil
	}
	dbPath := config.DatabasePath
	if dbPath == "" {
		dbPath = filepath.Join(homeDir, "workgraph.db")
	}
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil
	}
	defer db.Close()
	rows, err := db.Query(`SELECT DISTINCT json_extract(payload_json, '$.repo_path') FROM events
		WHERE type = 'git.commit' AND json_extract(payload_json, '$.repo_path') IS NOT NULL`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var repos []string
	for rows.Next() {
		var repo string
		if err := rows.Scan(&repo); err == nil && repo != "" {
			repos = append(repos, repo)
		}
	}
	return repos
}

func gitCommitAuthoredByLocalIdentity(payload string, emails map[string]bool) bool {
	if len(emails) == 0 {
		return true
	}
	return emails[strings.ToLower(gitCommitAuthorEmail(payload))]
}
