package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// resolveCommandConfiguration resolves and validates the installer
// configuration for a command invocation. Every command shares this single
// resolution path so behavior stays identical to the internals CSK-002/003
// already validate and test.
func resolveCommandConfiguration(environment map[string]string, scriptDir string) (Configuration, error) {
	cfg, err := ResolveConfiguration(environment, scriptDir)
	if err != nil {
		return Configuration{}, fmt.Errorf("resolve configuration: %w", err)
	}
	return cfg, nil
}

// runStatusCommand mirrors run_status: it reports the installed version
// against the remote branch HEAD. Unlike Bash, it does not perform a full
// clone to render a per-skill added/modified/removed diff; that reporting
// depth is intentionally out of scope for this slice (see task report).
func runStatusCommand(out io.Writer, environment map[string]string, scriptDir string) error {
	cfg, err := resolveCommandConfiguration(environment, scriptDir)
	if err != nil {
		return err
	}
	return runStatus(cfg, out)
}

func runStatus(cfg Configuration, out io.Writer) error {
	ctx := context.Background()
	remoteCommit, err := resolveRemoteHead(ctx, cfg.RepositoryURL, cfg.Branch)
	if err != nil {
		return err
	}

	state, err := ResolveInstalledState(cfg.StatePath, cfg.BackupRoot)
	if err != nil {
		fmt.Fprintln(out, "Installed version: none (no previous install recorded)")
		fmt.Fprintf(out, "Remote version: %s (branch: %s)\n", remoteCommit, cfg.Branch)
		fmt.Fprintln(out, "Run 'cskill install' to install the current skill-pack.")
		return nil
	}

	fmt.Fprintf(out, "Installed version: %s\n", state.SourceCommit)
	if state.RepositoryURL != "" {
		branchLabel := state.Branch
		if branchLabel == "" {
			branchLabel = "unknown"
		}
		fmt.Fprintf(out, "Installed source: %s (branch: %s)\n", state.RepositoryURL, branchLabel)
	}
	if state.SkillCount != "" {
		fmt.Fprintf(out, "Installed skills: %s\n", state.SkillCount)
	}
	fmt.Fprintf(out, "Remote version: %s (branch: %s)\n", remoteCommit, cfg.Branch)

	if state.RepositoryURL != "" && state.RepositoryURL != cfg.RepositoryURL {
		fmt.Fprintln(out, "Warning: configured source differs from the last install; comparison is by commit only.")
	}
	if state.Branch != "" && state.Branch != cfg.Branch {
		fmt.Fprintln(out, "Warning: configured branch differs from the last install; comparison is by commit only.")
	}

	if remoteCommit == state.SourceCommit {
		fmt.Fprintln(out, "Already up to date: installed version matches the remote version.")
		return nil
	}
	fmt.Fprintf(out, "A newer skill-pack version is available: %s -> %s\n", state.SourceCommit, remoteCommit)
	fmt.Fprintln(out, "Run 'cskill install' to update.")
	return nil
}

// runListCommand mirrors list_backups.
func runListCommand(out, errOut io.Writer, environment map[string]string, scriptDir string) error {
	cfg, err := resolveCommandConfiguration(environment, scriptDir)
	if err != nil {
		return err
	}
	return runList(cfg, out, errOut)
}

func runList(cfg Configuration, out, errOut io.Writer) error {
	info, err := os.Lstat(cfg.BackupRoot)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "No backups found in %s.\n", cfg.BackupRoot)
			return nil
		}
		return fmt.Errorf("inspect backup root: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("backup root is not a safe directory: %s", cfg.BackupRoot)
	}

	fmt.Fprintf(out, "Managed backups in %s\n", cfg.BackupRoot)
	entries, err := os.ReadDir(cfg.BackupRoot)
	if err != nil {
		return fmt.Errorf("read backup root: %w", err)
	}

	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		entryPath := filepath.Join(cfg.BackupRoot, entry.Name())
		entryInfo, statErr := os.Lstat(entryPath)
		if statErr != nil {
			continue
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 {
			fmt.Fprintf(errOut, "Skipping symbolic-link snapshot: %s\n", entry.Name())
			continue
		}
		if !entryInfo.IsDir() {
			continue
		}
		if !snapshotIDPattern.MatchString(entry.Name()) {
			fmt.Fprintf(errOut, "Skipping directory with an unsafe backup ID: %s\n", entry.Name())
			continue
		}
		ids = append(ids, entry.Name())
	}
	sort.Strings(ids)

	found := 0
	for _, id := range ids {
		metadata, err := ValidateSnapshot(filepath.Join(cfg.BackupRoot, id))
		if err != nil {
			fmt.Fprintf(errOut, "Skipping invalid snapshot %s: %v\n", id, err)
			continue
		}
		found++
		printSnapshotSummary(out, metadata)
	}
	if found == 0 {
		fmt.Fprintln(out, "No valid backups found.")
	}
	return nil
}

func printSnapshotSummary(out io.Writer, metadata SnapshotMetadata) {
	label := func(target SnapshotTarget) (state, count string) {
		if !target.Captured {
			return "not captured", "n/a"
		}
		state = "no"
		if target.Existed {
			state = "yes"
		}
		return state, strconv.Itoa(target.SkillCount)
	}
	openCodeState, openCodeCount := label(metadata.OpenCode)
	claudeState, claudeCount := label(metadata.Claude)
	agyState, agyCount := label(metadata.AGY)

	fmt.Fprintln(out, metadata.ID)
	fmt.Fprintf(out, "  UTC: %s | Reason: %s | Targets: OpenCode %s, Claude %s, AGY %s\n",
		metadata.CreatedUTC, metadata.Reason, openCodeState, claudeState, agyState)
	fmt.Fprintf(out, "  Skills: %s/%s/%s | Source commit: %s\n",
		openCodeCount, claudeCount, agyCount, metadata.SourceCommit)
}

// runInstallCommand mirrors run_install's core mechanics: clone, validate,
// stage, transact, and record installed state. It always performs a fresh
// install rather than replicating Bash's already-up-to-date short-circuit;
// see the task report for that scoped-out product decision.
func runInstallCommand(args []string, out io.Writer, environment map[string]string, scriptDir string) error {
	force := false
	switch len(args) {
	case 0:
	case 1:
		if args[0] != "--force" {
			return fmt.Errorf("install accepts only an optional --force argument")
		}
		force = true
	default:
		return fmt.Errorf("install accepts only an optional --force argument")
	}

	cfg, err := resolveCommandConfiguration(environment, scriptDir)
	if err != nil {
		return err
	}
	return runInstall(cfg, force, environment, out)
}

func runInstall(cfg Configuration, force bool, environment map[string]string, out io.Writer) error {
	ctx := context.Background()
	skillsDir, commit, cleanup, err := cloneSkillSource(ctx, cfg, environment)
	if err != nil {
		return err
	}
	defer cleanup()

	count, err := ValidateSkillTree(skillsDir, cfg.MinimumSkillCount, 0)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "Source commit: %s\n", commit)
	fmt.Fprintf(out, "Validated skills: %d\n", count)
	if force {
		fmt.Fprintln(out, "Force reinstall requested; proceeding regardless of the currently installed version.")
	}

	replacements := []stagedTarget{
		{Target: cfg.OpenCodeTarget, StagedPath: skillsDir},
		{Target: cfg.ClaudeTarget, StagedPath: skillsDir},
		{Target: cfg.AgyTarget, StagedPath: skillsDir},
	}
	snapshotID, err := runTransactionWithOptions(cfg, replacements, "install", commit, transactionHooks{})
	if err != nil {
		return err
	}

	state := InstalledState{
		RepositoryURL: cfg.RepositoryURL,
		Branch:        cfg.Branch,
		SourceCommit:  commit,
		SkillCount:    strconv.Itoa(count),
		InstalledUTC:  time.Now().UTC().Format("2006-01-02T15:04:05Z"),
	}
	if err := WriteInstalledState(cfg.StatePath, state); err != nil {
		return fmt.Errorf("install committed; installed state not recorded: %w", err)
	}

	fmt.Fprintf(out, "Backup snapshot: %s\n", snapshotID)
	fmt.Fprintf(out, "Installed %d skills from commit %s into OpenCode, Claude, and AGY.\n", count, commit)
	fmt.Fprintln(out, "This replacement removed existing skills from all three directories. Restart OpenCode, Claude, and AGY so they reload the installed skills.")
	return nil
}

// runRestoreCommand mirrors main's restore branch and run_restore: it
// requires an explicit, pattern-safe BACKUP_ID. Unlike Bash, it never falls
// back to an interactive TTY picker (menu/interactive_restore); see the task
// report for that scoped-out product decision.
func runRestoreCommand(args []string, out io.Writer, environment map[string]string, scriptDir string) error {
	if len(args) > 1 {
		return fmt.Errorf("restore accepts at most one BACKUP_ID")
	}
	cfg, err := resolveCommandConfiguration(environment, scriptDir)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("restore requires a BACKUP_ID; run 'cskill list' to see managed snapshots")
	}
	snapshotID := args[0]
	if !snapshotIDPattern.MatchString(snapshotID) {
		return fmt.Errorf("unsafe backup ID: %s", snapshotID)
	}
	return runRestore(cfg, snapshotID, out)
}

func runRestore(cfg Configuration, snapshotID string, out io.Writer) error {
	safetyID, err := restoreSnapshot(cfg, snapshotID)
	if err != nil {
		return err
	}

	metadata, metadataErr := ValidateSnapshot(filepath.Join(cfg.BackupRoot, snapshotID))
	if metadataErr == nil && commitPattern.MatchString(metadata.SourceCommit) {
		skillCount, countErr := countLiveSkillManifests(cfg.OpenCodeTarget)
		if countErr == nil {
			state := InstalledState{
				RepositoryURL: cfg.RepositoryURL,
				Branch:        cfg.Branch,
				SourceCommit:  metadata.SourceCommit,
				SkillCount:    strconv.Itoa(skillCount),
				InstalledUTC:  time.Now().UTC().Format("2006-01-02T15:04:05Z"),
			}
			if writeErr := WriteInstalledState(cfg.StatePath, state); writeErr != nil {
				return fmt.Errorf("restore committed; installed state not updated: %w", writeErr)
			}
		}
	}

	fmt.Fprintf(out, "Restored snapshot: %s\n", snapshotID)
	fmt.Fprintf(out, "Pre-restore safety snapshot: %s\n", safetyID)
	fmt.Fprintln(out, "Restart applications whose captured skill targets were restored. Uncaptured targets were left unchanged.")
	return nil
}

// countLiveSkillManifests mirrors count_skill_manifests: it counts direct
// child SKILL.md manifests under an already-managed live target, treating a
// missing or unsafe root as zero rather than an error.
func countLiveSkillManifests(root string) (int, error) {
	info, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, nil
	}
	return countSnapshotManifests(root)
}
