package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// gitWithoutUserConfiguration runs git with the same sanitized environment as
// install-skills.sh's git_without_user_configuration wrapper: no system or
// global git configuration is read, and hooks are disabled. dir, when
// non-empty, sets the working directory for the invocation.
func gitWithoutUserConfiguration(ctx context.Context, dir string, args ...string) ([]byte, error) {
	fullArgs := append([]string{"-c", "core.hooksPath=/dev/null"}, args...)
	cmd := exec.CommandContext(ctx, "git", fullArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
	}
	return stdout.Bytes(), nil
}

var remoteHeadPattern = regexp.MustCompile(`^([0-9a-f]{40})\s`)

// resolveRemoteHead mirrors get_remote_head: it queries the remote HEAD
// commit for a branch without cloning the repository.
func resolveRemoteHead(ctx context.Context, repositoryURL, branch string) (string, error) {
	output, err := gitWithoutUserConfiguration(ctx, "", "ls-remote", repositoryURL, branch)
	if err != nil {
		return "", fmt.Errorf("reach source repository to check for updates: %w", err)
	}
	match := remoteHeadPattern.FindSubmatch(output)
	if match == nil {
		return "", fmt.Errorf("resolve remote HEAD for branch %q in %s", branch, repositoryURL)
	}
	return string(match[1]), nil
}

// validateCloneStagingRoot mirrors validate_temporary_root: it resolves the
// configured temporary directory and rejects one that is relative, missing,
// the filesystem root, inside a protected .codegraph directory, or
// overlapping a managed or protected destination. It reads TMPDIR from the
// same injected environment as ResolveConfiguration, never the live process
// environment, so command behavior stays deterministic and testable.
func validateCloneStagingRoot(cfg Configuration, environment map[string]string) (string, error) {
	input := environment["TMPDIR"]
	if input == "" {
		input = os.TempDir()
	}
	if input == "~" {
		input = cfg.HomeDir
	} else if strings.HasPrefix(input, "~/") {
		input = filepath.Join(cfg.HomeDir, input[2:])
	}
	if !filepath.IsAbs(input) {
		return "", fmt.Errorf("temporary root must be absolute: %s", input)
	}
	if info, err := os.Stat(input); err != nil || !info.IsDir() {
		return "", fmt.Errorf("temporary directory does not exist: %s", input)
	}
	resolved, err := canonicalMissingPath(input)
	if err != nil {
		return "", fmt.Errorf("resolve temporary root: %w", err)
	}
	if resolved == string(filepath.Separator) {
		return "", fmt.Errorf("temporary root cannot be the filesystem root")
	}
	if err := rejectCodegraphPath(resolved, "Temporary root"); err != nil {
		return "", err
	}
	protected := append(protectedPaths(cfg.HomeDir), cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget, cfg.BackupRoot)
	if err := rejectOverlaps("Temporary root", resolved, protected); err != nil {
		return "", err
	}
	return resolved, nil
}

// cloneSkillSource mirrors clone_repository: a shallow, single-branch clone
// of the configured repository/branch into a fresh temporary staging
// directory. The caller must invoke the returned cleanup function once the
// cloned skills directory is no longer needed.
func cloneSkillSource(ctx context.Context, cfg Configuration, environment map[string]string) (skillsDir, commit string, cleanup func(), err error) {
	root, err := validateCloneStagingRoot(cfg, environment)
	if err != nil {
		return "", "", nil, err
	}
	stagingDir, err := os.MkdirTemp(root, "cskill-installer.")
	if err != nil {
		return "", "", nil, fmt.Errorf("create clone staging directory: %w", err)
	}
	cleanup = func() { _ = os.RemoveAll(stagingDir) }

	repositoryDir := filepath.Join(stagingDir, "repository")
	if _, err := gitWithoutUserConfiguration(ctx, "", "clone", "--quiet", "--depth", "1", "--single-branch",
		"--branch", cfg.Branch, cfg.RepositoryURL, repositoryDir); err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("clone skill source: %w", err)
	}
	output, err := gitWithoutUserConfiguration(ctx, repositoryDir, "rev-parse", "HEAD")
	if err != nil {
		cleanup()
		return "", "", nil, fmt.Errorf("resolve cloned source commit: %w", err)
	}
	commit = strings.TrimSpace(string(output))
	if !commitPattern.MatchString(commit) {
		cleanup()
		return "", "", nil, fmt.Errorf("unexpected source commit: %s", commit)
	}
	return filepath.Join(repositoryDir, "skills"), commit, cleanup, nil
}
