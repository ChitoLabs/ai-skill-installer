package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveConfigurationEnvironmentPrecedence(t *testing.T) {
	home := t.TempDir()
	config := t.TempDir()
	xdg := t.TempDir()
	claudeConfig := t.TempDir()
	values := map[string]string{
		"HOME":                       home,
		"OPENCODE_SKILLS_DIR":        "~/explicit-opencode/skills",
		"OPENCODE_CONFIG_DIR":        filepath.Join(config, "opencode"),
		"XDG_CONFIG_HOME":            xdg,
		"CLAUDE_CONFIG_DIR":          claudeConfig,
		"AGY_SKILLS_DIR":             filepath.Join(home, "agy-explicit", "skills"),
		"SKILL_PACK_REPOSITORY_URL":  "https://example.test/skills",
		"SKILL_PACK_BRANCH":          "stable",
		"SKILL_PACK_MIN_SKILL_COUNT": "7",
		"SKILL_BACKUP_DIR":           "~/backup",
		"SKILL_INSTALLER_LOCK_FILE":  "~/installer.lock",
		"SKILL_INSTALLER_STATE_FILE": "~/installer.state",
	}
	cfg, err := ResolveConfiguration(values, filepath.Join(home, "app"))
	if err != nil {
		t.Fatalf("ResolveConfiguration() error = %v", err)
	}
	wants := map[string]string{
		"repository": "https://example.test/skills",
		"branch":     "stable",
		"opencode":   filepath.Join(home, "explicit-opencode", "skills"),
		"claude":     filepath.Join(claudeConfig, "skills"),
		"agy":        filepath.Join(home, "agy-explicit", "skills"),
		"backup":     filepath.Join(home, "backup"),
		"lock":       filepath.Join(home, "installer.lock"),
		"state":      filepath.Join(home, "installer.state"),
	}
	got := map[string]string{
		"repository": cfg.RepositoryURL,
		"branch":     cfg.Branch,
		"opencode":   cfg.OpenCodeTarget,
		"claude":     cfg.ClaudeTarget,
		"agy":        cfg.AgyTarget,
		"backup":     cfg.BackupRoot,
		"lock":       cfg.LockPath,
		"state":      cfg.StatePath,
	}
	for key, want := range wants {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
	if cfg.MinimumSkillCount != 7 {
		t.Errorf("minimum count = %d, want 7", cfg.MinimumSkillCount)
	}
}

func TestResolveConfigurationFallbackPrecedence(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	base := map[string]string{"HOME": home, "XDG_CONFIG_HOME": xdg}
	tests := []struct {
		name string
		set  map[string]string
		want string
	}{
		{name: "OpenCode config before XDG", set: map[string]string{"OPENCODE_CONFIG_DIR": "/chosen/config"}, want: "/chosen/config/skills"},
		{name: "XDG before home default", want: filepath.Join(xdg, "opencode", "skills")},
		{name: "home default", set: map[string]string{"XDG_CONFIG_HOME": ""}, want: filepath.Join(home, ".config", "opencode", "skills")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := cloneEnvironment(base)
			for key, value := range test.set {
				values[key] = value
			}
			cfg, err := ResolveConfiguration(values, filepath.Join(home, "app"))
			if err != nil {
				t.Fatalf("ResolveConfiguration() error = %v", err)
			}
			if cfg.OpenCodeTarget != test.want {
				t.Errorf("OpenCodeTarget = %q, want %q", cfg.OpenCodeTarget, test.want)
			}
		})
	}
}

func TestResolveConfigurationRejectsInvalidInputs(t *testing.T) {
	home := t.TempDir()
	base := map[string]string{"HOME": home}
	tests := []struct {
		name string
		set  map[string]string
		want string
	}{
		{name: "repository begins with dash", set: map[string]string{"SKILL_PACK_REPOSITORY_URL": "-bad"}, want: "begin with a dash"},
		{name: "zero minimum", set: map[string]string{"SKILL_PACK_MIN_SKILL_COUNT": "0"}, want: "positive integer"},
		{name: "leading-zero minimum", set: map[string]string{"SKILL_PACK_MIN_SKILL_COUNT": "01"}, want: "positive integer"},
		{name: "non-integer minimum", set: map[string]string{"SKILL_PACK_MIN_SKILL_COUNT": "2.5"}, want: "positive integer"},
		{name: "relative target", set: map[string]string{"OPENCODE_SKILLS_DIR": "relative/skills"}, want: "must be absolute"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := cloneEnvironment(base)
			for key, value := range test.set {
				values[key] = value
			}
			_, err := ResolveConfiguration(values, filepath.Join(home, "app"))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("ResolveConfiguration() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestValidateConfigurationRejectsUnsafePaths(t *testing.T) {
	home := t.TempDir()
	base := map[string]string{"HOME": home}
	tests := []struct {
		name string
		set  map[string]string
		want string
	}{
		{name: "root target", set: map[string]string{"OPENCODE_SKILLS_DIR": "/"}, want: "filesystem root"},
		{name: "wrong target suffix", set: map[string]string{"OPENCODE_SKILLS_DIR": filepath.Join(home, "opencode", "other")}, want: "end in /skills"},
		{name: "overlapping targets", set: map[string]string{"CLAUDE_SKILLS_DIR": filepath.Join(home, ".config", "opencode", "skills", "nested", "skills")}, want: "distinct and non-overlapping"},
		{name: "backup overlaps target", set: map[string]string{"SKILL_BACKUP_DIR": filepath.Join(home, ".config", "opencode")}, want: "overlaps"},
		{name: "state overlaps target", set: map[string]string{"SKILL_INSTALLER_STATE_FILE": filepath.Join(home, ".config", "opencode", "skills", "state")}, want: "overlaps"},
		{name: "path in codegraph", set: map[string]string{"SKILL_INSTALLER_LOCK_FILE": filepath.Join(home, "cache", ".codegraph", "lock")}, want: ".codegraph"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := cloneEnvironment(base)
			for key, value := range test.set {
				values[key] = value
			}
			_, err := ResolveConfiguration(values, filepath.Join(home, "app"))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("ResolveConfiguration() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestValidateConfigurationRejectsSymlinksAndProtectedTrees(t *testing.T) {
	home := t.TempDir()
	actual := filepath.Join(home, "actual")
	if err := os.MkdirAll(actual, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(home, "linked")
	if err := os.Symlink(actual, link); err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(home, ".config", "opencode", "skills", "nested", ".codegraph")
	if err := os.MkdirAll(protected, 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		set  map[string]string
		want string
	}{
		{name: "final target symlink", set: map[string]string{"OPENCODE_SKILLS_DIR": link}, want: "symbolic link"},
		{name: "target contains codegraph", set: map[string]string{"OPENCODE_SKILLS_DIR": filepath.Join(home, ".config", "opencode", "skills")}, want: ".codegraph"},
		{name: "target overlaps protected commands", set: map[string]string{"OPENCODE_SKILLS_DIR": filepath.Join(home, ".config", "opencode", "commands", "skills")}, want: "overlaps protected path"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]string{"HOME": home}
			for key, value := range test.set {
				values[key] = value
			}
			_, err := ResolveConfiguration(values, filepath.Join(home, "app"))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("ResolveConfiguration() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestValidateConfigurationRejectsUnsafeExistingPaths(t *testing.T) {
	home := t.TempDir()
	regularFile := filepath.Join(home, "not-a-directory")
	if err := os.WriteFile(regularFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	backupTarget := filepath.Join(home, "backup-target")
	if err := os.Mkdir(backupTarget, 0o755); err != nil {
		t.Fatal(err)
	}
	backupLink := filepath.Join(home, "backup-link")
	if err := os.Symlink(backupTarget, backupLink); err != nil {
		t.Fatal(err)
	}
	stateDirectory := filepath.Join(home, "state-directory")
	if err := os.Mkdir(stateDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		set  map[string]string
		want string
	}{
		{name: "target is a file", set: map[string]string{"OPENCODE_SKILLS_DIR": filepath.Join(regularFile, "skills")}, want: "not a directory"},
		{name: "backup root is symlink", set: map[string]string{"SKILL_BACKUP_DIR": backupLink}, want: "symbolic link"},
		{name: "state path is a directory", set: map[string]string{"SKILL_INSTALLER_STATE_FILE": stateDirectory}, want: "not a regular file"},
		{name: "backup contains codegraph directory", set: map[string]string{"SKILL_BACKUP_DIR": filepath.Join(home, "archive")}, want: ".codegraph"},
	}
	if err := os.MkdirAll(filepath.Join(home, "archive", "nested", ".codegraph"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]string{"HOME": home}
			for key, value := range test.set {
				values[key] = value
			}
			_, err := ResolveConfiguration(values, filepath.Join(home, "app"))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("ResolveConfiguration() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestResolveConfigurationDoesNotCreateManagedPaths(t *testing.T) {
	home := t.TempDir()
	scriptDir := filepath.Join(home, "app")
	values := map[string]string{"HOME": home}
	if _, err := ResolveConfiguration(values, scriptDir); err != nil {
		t.Fatalf("ResolveConfiguration() error = %v", err)
	}
	for _, path := range []string{
		filepath.Join(home, ".config", "opencode", "skills"),
		filepath.Join(home, ".claude", "skills"),
		filepath.Join(home, ".gemini", "config", "skills"),
		filepath.Join(scriptDir, "BKOld"),
		filepath.Join(scriptDir, ".install-skills.lock"),
		filepath.Join(scriptDir, ".installed-skills.state"),
	} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("configuration resolution changed or created %s (Lstat error = %v)", path, err)
		}
	}
}

func TestResolveConfigurationDefaultsManagedPathsToScriptDir(t *testing.T) {
	home := t.TempDir()
	scriptDir := filepath.Join(home, "app")
	cfg, err := ResolveConfiguration(map[string]string{"HOME": home}, scriptDir)
	if err != nil {
		t.Fatalf("ResolveConfiguration() error = %v", err)
	}
	wants := map[string]string{
		"backup root": filepath.Join(scriptDir, "BKOld"),
		"lock path":   filepath.Join(scriptDir, ".install-skills.lock"),
		"state path":  filepath.Join(scriptDir, ".installed-skills.state"),
	}
	got := map[string]string{
		"backup root": cfg.BackupRoot,
		"lock path":   cfg.LockPath,
		"state path":  cfg.StatePath,
	}
	for label, want := range wants {
		if got[label] != want {
			t.Errorf("%s = %q, want %q", label, got[label], want)
		}
	}
}

func TestNormalizeManagedPathExpandsHomeAndCanonicalizesMissingTail(t *testing.T) {
	home := t.TempDir()
	parent := filepath.Join(home, "existing")
	if err := os.Mkdir(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := normalizeManagedPath("~/existing/../new/", "path", home)
	if err != nil {
		t.Fatalf("normalizeManagedPath() error = %v", err)
	}
	want := filepath.Join(home, "new")
	if got != want {
		t.Errorf("normalized path = %q, want %q", got, want)
	}
}

func cloneEnvironment(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}
