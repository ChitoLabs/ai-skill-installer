package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// InstalledState contains the key/value fields written by the Bash installer.
// SkillCount and InstalledUTC are strings because Bash's loader treats them as
// optional, opaque values rather than validating their formats.
type InstalledState struct {
	RepositoryURL string
	Branch        string
	SourceCommit  string
	SkillCount    string
	InstalledUTC  string
}

var installedStateKeys = map[string]bool{
	"repository_url": true,
	"branch":         true,
	"source_commit":  true,
	"skill_count":    true,
	"installed_utc":  true,
}

// ParseInstalledState follows load_installed_state: repository, branch, and
// source_commit are required; skill_count and installed_utc are optional.
func ParseInstalledState(reader io.Reader) (InstalledState, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return InstalledState{}, fmt.Errorf("read installed state: %w", err)
	}
	values := make(map[string]string, len(installedStateKeys))
	for lineNumber, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return InstalledState{}, fmt.Errorf("malformed installed state at line %d", lineNumber+1)
		}
		if !installedStateKeys[key] {
			return InstalledState{}, fmt.Errorf("unknown installed state key %q", key)
		}
		if _, exists := values[key]; exists {
			return InstalledState{}, fmt.Errorf("duplicate installed state key %q", key)
		}
		values[key] = value
	}
	for _, key := range []string{"repository_url", "branch", "source_commit"} {
		if value, exists := values[key]; !exists || value == "" {
			return InstalledState{}, fmt.Errorf("missing or empty installed state key %q", key)
		}
	}
	if !commitPattern.MatchString(values["source_commit"]) {
		return InstalledState{}, fmt.Errorf("invalid installed state source_commit")
	}
	return InstalledState{
		RepositoryURL: values["repository_url"],
		Branch:        values["branch"],
		SourceCommit:  values["source_commit"],
		SkillCount:    values["skill_count"],
		InstalledUTC:  values["installed_utc"],
	}, nil
}

// SerializeInstalledState emits the exact ordered state format written by
// write_installed_state in the Bash installer.
func SerializeInstalledState(state InstalledState) ([]byte, error) {
	if state.RepositoryURL == "" || state.Branch == "" || !commitPattern.MatchString(state.SourceCommit) {
		return nil, fmt.Errorf("invalid installed state")
	}
	for _, value := range []string{state.RepositoryURL, state.Branch, state.SkillCount, state.InstalledUTC} {
		if strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("installed state values cannot contain line breaks")
		}
	}
	return []byte(fmt.Sprintf("repository_url=%s\nbranch=%s\nsource_commit=%s\nskill_count=%s\ninstalled_utc=%s\n",
		state.RepositoryURL, state.Branch, state.SourceCommit, state.SkillCount, state.InstalledUTC)), nil
}

// ResolveInstalledState mirrors resolve_installed_commit: a valid state file
// takes precedence; otherwise it returns the newest valid install snapshot's
// commit, skipping malformed, unsafe, or non-install snapshots.
func ResolveInstalledState(statePath, backupRoot string) (InstalledState, error) {
	if info, err := os.Lstat(statePath); err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		file, openErr := os.Open(statePath)
		if openErr == nil {
			state, parseErr := ParseInstalledState(file)
			closeErr := file.Close()
			if parseErr == nil && closeErr == nil {
				return state, nil
			}
		}
	}
	commit, err := latestValidInstallCommit(backupRoot)
	if err != nil {
		return InstalledState{}, err
	}
	return InstalledState{SourceCommit: commit}, nil
}

func latestValidInstallCommit(backupRoot string) (string, error) {
	info, err := os.Lstat(backupRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("no valid install snapshot found")
	}
	entries, err := os.ReadDir(backupRoot)
	if err != nil {
		return "", fmt.Errorf("read backup root: %w", err)
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() && snapshotIDPattern.MatchString(entry.Name()) {
			ids = append(ids, entry.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	for _, id := range ids {
		metadata, err := ValidateSnapshot(filepath.Join(backupRoot, id))
		if err == nil && metadata.Reason == "install" && commitPattern.MatchString(metadata.SourceCommit) {
			return metadata.SourceCommit, nil
		}
	}
	return "", fmt.Errorf("no valid install snapshot found")
}
