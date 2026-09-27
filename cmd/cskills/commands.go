package main

import (
	"context"
	"errors"
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

// runStatusCommand mirrors run_status (install-skills.sh:1351-1409): it
// reports the installed version against the remote branch HEAD and, like
// Bash, clones the source to render a per-skill added/modified/removed diff
// (summarize_skill_changes, install-skills.sh:624-676) and to warn about
// local drift when the installed commit already matches the remote one
// (targets_match_source, install-skills.sh:678-683).
func runStatusCommand(out io.Writer, environment map[string]string, scriptDir string) error {
	cfg, err := resolveCommandConfiguration(environment, scriptDir)
	if err != nil {
		return err
	}
	return runStatus(cfg, environment, out)
}

func runStatus(cfg Configuration, environment map[string]string, out io.Writer) error {
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

	reference := referenceTarget(cfg)

	if remoteCommit == state.SourceCommit {
		if reference == "" {
			fmt.Fprintln(out, "Already up to date: installed version matches the remote version.")
			return nil
		}
		skillsDir, _, cleanup, err := cloneSkillSource(ctx, cfg, environment)
		if err != nil {
			return err
		}
		defer cleanup()
		if _, err := ValidateSkillTree(skillsDir, cfg.MinimumSkillCount, 0); err != nil {
			return err
		}
		fmt.Fprintf(out, "Skill diff against %s:\n", reference)
		summary, err := summarizeSkillChanges(reference, skillsDir)
		if err != nil {
			return err
		}
		writeSkillChangeSummary(out, summary)
		if targetsMatchSource(skillsDir, reference) {
			fmt.Fprintln(out, "Already up to date: installed version matches the remote version.")
		} else {
			fmt.Fprintln(out, "Warning: same commit as installed, but local targets differ (manual drift). Re-run install to restore the canonical tree.")
		}
		return nil
	}

	fmt.Fprintf(out, "A newer skill-pack version is available: %s -> %s\n", state.SourceCommit, remoteCommit)
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
	if reference != "" {
		fmt.Fprintf(out, "Skill diff against %s:\n", reference)
		summary, err := summarizeSkillChanges(reference, skillsDir)
		if err != nil {
			return err
		}
		writeSkillChangeSummary(out, summary)
	} else {
		fmt.Fprintf(out, "No installed targets found; all %d skills would be new.\n", count)
	}
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

// runInstallCommand mirrors run_install (install-skills.sh:1411-1493): lock,
// clone, validate, skip an idempotent reinstall (targets_match_source,
// install-skills.sh:1425-1440), stage, transact, and record installed state.
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

// errInstallAlreadyUpToDate signals, from inside the install preparation
// callback, that the configured commit/repository/branch already match the
// installed state and the live targets already match the cloned source
// (targets_match_source), so run_install's install-skills.sh:1429 skip
// applies: no snapshot, no transaction, and no state write.
var errInstallAlreadyUpToDate = errors.New("skill pack already up to date")

// runInstall mirrors run_install (install-skills.sh:1411-1493). The clone,
// idempotence check, and skill-tree validation all happen inside the
// preparation callback so they run only after the installer lock is
// acquired and it stays held, uninterrupted, through the transaction and
// the installed-state write (install-skills.sh:1413-1414, 1489), instead of
// acquiring the lock a second time.
func runInstall(cfg Configuration, force bool, environment map[string]string, out io.Writer) error {
	ctx := context.Background()
	var sourceCommit string
	var skillCount int

	prepare := func() ([]stagedTarget, string, func(), error) {
		skillsDir, commit, cleanup, err := cloneSkillSource(ctx, cfg, environment)
		if err != nil {
			return nil, "", nil, err
		}
		count, err := ValidateSkillTree(skillsDir, cfg.MinimumSkillCount, 0)
		if err != nil {
			cleanup()
			return nil, "", nil, err
		}
		sourceCommit = commit
		skillCount = count
		fmt.Fprintf(out, "Source commit: %s\n", commit)
		fmt.Fprintf(out, "Validated skills: %d\n", count)
		if force {
			fmt.Fprintln(out, "Force reinstall requested; proceeding regardless of the currently installed version.")
		}

		installedState, stateErr := ResolveInstalledState(cfg.StatePath, cfg.BackupRoot)
		sameVersion := stateErr == nil &&
			installedState.SourceCommit == commit &&
			installedState.RepositoryURL == cfg.RepositoryURL &&
			installedState.Branch == cfg.Branch
		if sameVersion {
			reference := referenceTarget(cfg)
			if reference != "" {
				if targetsMatchSource(skillsDir, reference) {
					if !force {
						cleanup()
						return nil, "", nil, errInstallAlreadyUpToDate
					}
				} else {
					fmt.Fprintln(out, "Warning: same commit as installed, but local targets differ (manual drift). Proceeding with reinstall.")
				}
			}
		} else if stateErr == nil {
			// Mirrors run_install's pre-upgrade summary (install-skills.sh:1441-1447):
			// a previously installed version was resolved but it differs from the
			// source commit/repository/branch, so log the added/modified/removed
			// skill diff against the current reference target before replacing it.
			// A fresh install (stateErr != nil, install-skills.sh:1448-1450) never
			// reaches this branch, matching Bash's "none (fresh install)" path.
			if reference := referenceTarget(cfg); reference != "" {
				fmt.Fprintf(out, "Skill changes vs %s:\n", reference)
				summary, summaryErr := summarizeSkillChanges(reference, skillsDir)
				if summaryErr != nil {
					cleanup()
					return nil, "", nil, summaryErr
				}
				writeSkillChangeSummary(out, summary)
			}
		}

		replacements := []stagedTarget{
			{Target: cfg.OpenCodeTarget, StagedPath: skillsDir},
			{Target: cfg.ClaudeTarget, StagedPath: skillsDir},
			{Target: cfg.AgyTarget, StagedPath: skillsDir},
		}
		return replacements, commit, cleanup, nil
	}

	postCommit := func(commit, _ string) error {
		state := InstalledState{
			RepositoryURL: cfg.RepositoryURL,
			Branch:        cfg.Branch,
			SourceCommit:  commit,
			SkillCount:    strconv.Itoa(skillCount),
			InstalledUTC:  time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		}
		if err := WriteInstalledState(cfg.StatePath, state); err != nil {
			return fmt.Errorf("install committed; installed state not recorded: %w", err)
		}
		return nil
	}

	snapshotID, err := runTransactionWithPreparation(cfg, nil, "install", "unknown", transactionHooks{}, prepare, postCommit)
	if err != nil {
		if errors.Is(err, errInstallAlreadyUpToDate) {
			fmt.Fprintf(out, "Already up to date: version %s is installed in all targets.\n", sourceCommit)
			fmt.Fprintln(out, "Use 'cskill install --force' to reinstall the same version.")
			return nil
		}
		return err
	}

	fmt.Fprintf(out, "Backup snapshot: %s\n", snapshotID)
	fmt.Fprintf(out, "Installed %d skills from commit %s into OpenCode, Claude, and AGY.\n", skillCount, sourceCommit)
	fmt.Fprintln(out, "This replacement removed existing skills from all three directories. Restart OpenCode, Claude, and AGY so they reload the installed skills.")
	return nil
}

// runRestoreCommand mirrors main's restore branch and run_restore: it
// requires an explicit, pattern-safe BACKUP_ID. This is an intentional
// product difference from Bash: interactive restore (menu/interactive_restore)
// is not provided, so restore always requires an explicit BACKUP_ID.
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

// runRestore mirrors run_restore (install-skills.sh:1495-1567). The installed
// state write now happens inside restoreSnapshot's postCommit callback, still
// under the installer lock acquired by runTransactionWithPreparation, instead
// of after restoreSnapshot returns and the lock has been released.
func runRestore(cfg Configuration, snapshotID string, out io.Writer) error {
	safetyID, err := restoreSnapshot(cfg, snapshotID)
	if err != nil {
		return err
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
