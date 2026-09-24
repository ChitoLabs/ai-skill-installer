package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultRepositoryURL = "https://github.com/ChitoLabs/ai-skill-pack"
	defaultBranch        = "main"
	defaultMinimumSkills = 100
)

// Configuration contains resolved installer paths. Resolving it performs only
// validation and filesystem reads; it never creates or changes managed paths.
type Configuration struct {
	RepositoryURL     string
	Branch            string
	MinimumSkillCount int
	OpenCodeTarget    string
	ClaudeTarget      string
	AgyTarget         string
	BackupRoot        string
	LockPath          string
	StatePath         string
}

// ResolveConfiguration applies the installer's environment precedence and
// validates every destination before returning a usable configuration.
func ResolveConfiguration(environment map[string]string, scriptDir string) (Configuration, error) {
	get := func(key, fallback string) string {
		if value := environment[key]; value != "" {
			return value
		}
		return fallback
	}

	home := environment["HOME"]
	if home == "" {
		return Configuration{}, fmt.Errorf("HOME must be set")
	}
	if !filepath.IsAbs(home) {
		return Configuration{}, fmt.Errorf("HOME must be absolute: %s", home)
	}
	home, err := canonicalMissingPath(home)
	if err != nil {
		return Configuration{}, fmt.Errorf("resolve HOME: %w", err)
	}
	if !filepath.IsAbs(scriptDir) {
		return Configuration{}, fmt.Errorf("script directory must be absolute: %s", scriptDir)
	}
	scriptDir, err = canonicalMissingPath(scriptDir)
	if err != nil {
		return Configuration{}, fmt.Errorf("resolve script directory: %w", err)
	}

	minimumText := get("SKILL_PACK_MIN_SKILL_COUNT", strconv.Itoa(defaultMinimumSkills))
	minimum, err := strconv.Atoi(minimumText)
	validMinimumSyntax := minimumText != "" && minimumText[0] >= '1' && minimumText[0] <= '9'
	for index := 1; index < len(minimumText); index++ {
		digit := minimumText[index]
		validMinimumSyntax = validMinimumSyntax && digit >= '0' && digit <= '9'
	}
	if err != nil || minimum <= 0 || !validMinimumSyntax {
		return Configuration{}, fmt.Errorf("SKILL_PACK_MIN_SKILL_COUNT must be a positive integer")
	}
	repository := get("SKILL_PACK_REPOSITORY_URL", defaultRepositoryURL)
	if strings.HasPrefix(repository, "-") {
		return Configuration{}, fmt.Errorf("repository URL cannot begin with a dash")
	}
	if repository == "" {
		return Configuration{}, fmt.Errorf("repository URL cannot be empty")
	}
	branch := get("SKILL_PACK_BRANCH", defaultBranch)
	if branch == "" {
		return Configuration{}, fmt.Errorf("branch cannot be empty")
	}

	opencodeInput := get("OPENCODE_SKILLS_DIR", "")
	if opencodeInput == "" {
		if configDir := get("OPENCODE_CONFIG_DIR", ""); configDir != "" {
			opencodeInput = filepath.Join(configDir, "skills")
		} else if xdg := get("XDG_CONFIG_HOME", ""); xdg != "" {
			opencodeInput = filepath.Join(xdg, "opencode", "skills")
		} else {
			opencodeInput = filepath.Join(home, ".config", "opencode", "skills")
		}
	}
	claudeInput := get("CLAUDE_SKILLS_DIR", "")
	if claudeInput == "" {
		claudeInput = filepath.Join(get("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude")), "skills")
	}
	agyInput := get("AGY_SKILLS_DIR", filepath.Join(home, ".gemini", "config", "skills"))

	cfg := Configuration{
		RepositoryURL:     repository,
		Branch:            branch,
		MinimumSkillCount: minimum,
	}
	paths := []struct {
		input string
		label string
		out   *string
	}{
		{opencodeInput, "OpenCode target", &cfg.OpenCodeTarget},
		{claudeInput, "Claude target", &cfg.ClaudeTarget},
		{agyInput, "AGY target", &cfg.AgyTarget},
		{get("SKILL_BACKUP_DIR", filepath.Join(scriptDir, "BKOld")), "Backup root", &cfg.BackupRoot},
		{get("SKILL_INSTALLER_LOCK_FILE", filepath.Join(scriptDir, ".install-skills.lock")), "Installer lock path", &cfg.LockPath},
		{get("SKILL_INSTALLER_STATE_FILE", filepath.Join(scriptDir, ".installed-skills.state")), "Installer state path", &cfg.StatePath},
	}
	for _, path := range paths {
		resolved, err := normalizeManagedPath(path.input, path.label, home)
		if err != nil {
			return Configuration{}, err
		}
		*path.out = resolved
	}
	if err := cfg.validatePaths(home); err != nil {
		return Configuration{}, err
	}
	return cfg, nil
}

func (cfg Configuration) validatePaths(home string) error {
	protected := protectedPaths(home)
	targets := []struct {
		label string
		path  string
	}{
		{"OpenCode target", cfg.OpenCodeTarget},
		{"Claude target", cfg.ClaudeTarget},
		{"AGY target", cfg.AgyTarget},
	}
	for _, target := range targets {
		if filepath.Base(target.path) != "skills" {
			return fmt.Errorf("%s must end in /skills: %s", target.label, target.path)
		}
		if err := validateDirectoryPath(target.path, target.label); err != nil {
			return err
		}
		if err := rejectOverlaps(target.label, target.path, protected); err != nil {
			return err
		}
	}
	for i := 0; i < len(targets); i++ {
		for j := i + 1; j < len(targets); j++ {
			if pathsOverlap(targets[i].path, targets[j].path) {
				return fmt.Errorf("all skill targets must be distinct and non-overlapping: %s and %s", targets[i].path, targets[j].path)
			}
		}
	}

	if err := validateDirectoryPath(cfg.BackupRoot, "Backup root"); err != nil {
		return err
	}
	if err := rejectOverlaps("Backup root", cfg.BackupRoot, append(append([]string{}, protected...), cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget)); err != nil {
		return err
	}
	for _, filePath := range []struct{ label, path string }{{"Installer lock path", cfg.LockPath}, {"Installer state path", cfg.StatePath}} {
		if err := validateFilePath(filePath.path, filePath.label); err != nil {
			return err
		}
		if err := rejectOverlaps(filePath.label, filePath.path, append(append([]string{}, protected...), cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget, cfg.BackupRoot)); err != nil {
			return err
		}
	}
	return nil
}

func normalizeManagedPath(input, label, home string) (string, error) {
	if input == "~" {
		input = home
	} else if strings.HasPrefix(input, "~/") {
		input = filepath.Join(home, input[2:])
	}
	if !filepath.IsAbs(input) {
		return "", fmt.Errorf("%s must be absolute: %s", label, input)
	}
	input = filepath.Clean(input)
	if info, err := os.Lstat(input); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%s cannot be a symbolic link: %s", label, input)
	} else if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	resolved, err := canonicalMissingPath(input)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	if resolved == string(filepath.Separator) {
		return "", fmt.Errorf("%s cannot be the filesystem root", label)
	}
	return resolved, nil
}

func canonicalMissingPath(path string) (string, error) {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	missing := make([]string, 0, 4)
	ancestor := path
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", fmt.Errorf("no existing ancestor for %s", path)
		}
		missing = append(missing, filepath.Base(ancestor))
		ancestor = parent
	}
	resolved, err := filepath.EvalSymlinks(ancestor)
	if err != nil {
		return "", err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		resolved = filepath.Join(resolved, missing[i])
	}
	return filepath.Clean(resolved), nil
}

func validateDirectoryPath(path, label string) error {
	if err := rejectCodegraphPath(path, label); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err == nil && !info.IsDir() {
		return fmt.Errorf("%s is not a directory: %s", label, path)
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	return nil
}

func validateFilePath(path, label string) error {
	if err := rejectCodegraphPath(path, label); err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file: %s", label, path)
	}
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("inspect %s: %w", label, err)
	}
	return nil
}

func rejectCodegraphPath(path, label string) error {
	for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
		if part == ".codegraph" {
			return fmt.Errorf("%s cannot be inside a .codegraph directory: %s", label, path)
		}
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("inspect %s: %w", label, err)
		}
		return nil
	}
	err = filepath.WalkDir(path, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current != path && entry.IsDir() && entry.Name() == ".codegraph" {
			return fmt.Errorf("%s contains a protected .codegraph directory: %s", label, current)
		}
		return nil
	})
	if err != nil {
		return err
	}
	return nil
}

func rejectOverlaps(label, path string, protected []string) error {
	for _, other := range protected {
		if pathsOverlap(path, other) {
			return fmt.Errorf("%s overlaps protected path: %s", label, other)
		}
	}
	return nil
}

func pathsOverlap(first, second string) bool {
	return first == second || withinPath(first, second) || withinPath(second, first)
}

func withinPath(candidate, root string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func protectedPaths(home string) []string {
	paths := []string{
		filepath.Join(home, ".config", "opencode", "commands"),
		filepath.Join(home, ".engram"),
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".gemini", "antigravity-cli", "skills"),
	}
	for i, path := range paths {
		if resolved, err := canonicalMissingPath(path); err == nil {
			paths[i] = resolved
		}
	}
	return paths
}
