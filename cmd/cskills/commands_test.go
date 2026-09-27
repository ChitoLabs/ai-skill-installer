package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunStatusReportsNoInstallAndRemoteVersion(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL = "file://" + repository
	cfg.Branch = "main"

	var out bytes.Buffer
	if err := runStatus(cfg, commandEnvironment(cfg), &out); err != nil {
		t.Fatalf("runStatus() error = %v", err)
	}
	if !strings.Contains(out.String(), "Installed version: none") {
		t.Errorf("status output = %q, want no-install message", out.String())
	}
	if !strings.Contains(out.String(), "Remote version:") {
		t.Errorf("status output = %q, want remote version", out.String())
	}
}

func TestGoInstallFromLocalRepositoryIsAcceptedByBashStatus(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL = "file://" + repository
	cfg.Branch = "main"
	cfg.MinimumSkillCount = 1

	var installOut bytes.Buffer
	if err := runInstall(cfg, false, commandEnvironment(cfg), &installOut); err != nil {
		t.Fatalf("runInstall() error = %v\n%s", err, installOut.String())
	}
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		for _, skill := range []string{"alpha", "beta"} {
			if _, err := os.Stat(filepath.Join(target, skill, "SKILL.md")); err != nil {
				t.Errorf("install did not populate %s/%s: %v", target, skill, err)
			}
		}
	}
	if _, err := os.Stat(cfg.StatePath); err != nil {
		t.Fatalf("install did not write state file: %v", err)
	}

	var statusOut bytes.Buffer
	if err := runStatus(cfg, commandEnvironment(cfg), &statusOut); err != nil {
		t.Fatalf("runStatus() error = %v", err)
	}
	if !strings.Contains(statusOut.String(), "Already up to date") {
		t.Errorf("Go status after install = %q, want up-to-date report", statusOut.String())
	}

	command := exec.Command("bash", bashInstallerPath(t), "status")
	command.Env = bashInstallEnvironment(cfg, root, repository)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("bash status rejected Go-written state: %v\n%s", err, output)
	}
	if strings.Contains(string(output), "Installed version: none") {
		t.Errorf("bash status did not recognize Go-written installed state: %s", output)
	}
}

func TestRunInstallLockContentionDoesNotMutate(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL = "file://" + repository
	cfg.Branch = "main"
	cfg.MinimumSkillCount = 1
	targets := []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget}
	for _, target := range targets {
		writeRestoreTree(t, target, "before-install")
	}
	before := make(map[string][32]byte, len(targets))
	for _, target := range targets {
		fingerprint, err := fingerprintTree(target)
		if err != nil {
			t.Fatal(err)
		}
		before[target] = fingerprint
	}

	lock, err := acquireInstallerLock(cfg.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()

	environment := commandEnvironment(cfg)
	var out bytes.Buffer
	if err := runInstall(cfg, false, environment, &out); !errors.Is(err, errInstallerLockContended) {
		t.Fatalf("runInstall() error = %v, want lock contention", err)
	}
	for _, target := range targets {
		got, err := fingerprintTree(target)
		if err != nil || got != before[target] {
			t.Errorf("install mutated %s while the lock was held: err=%v", target, err)
		}
	}
	if _, err := os.Lstat(cfg.StatePath); !os.IsNotExist(err) {
		t.Errorf("install wrote installed state while the lock was contended")
	}
	if _, err := os.Lstat(cfg.BackupRoot); !os.IsNotExist(err) {
		t.Errorf("install created a backup snapshot while the lock was contended")
	}
	// Lock contention must fail before clone_repository's Go equivalent ever
	// runs (install-skills.sh:1413-1414: acquire_installer_lock precedes
	// clone_repository), so no clone staging directory should exist.
	stagingEntries, err := os.ReadDir(environment["TMPDIR"])
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range stagingEntries {
		if strings.HasPrefix(entry.Name(), "cskill-installer.") {
			t.Errorf("install cloned the source before the lock was acquired: found staging dir %s", entry.Name())
		}
	}
}

func TestRunListReportsEmptyThenPopulatedSnapshots(t *testing.T) {
	cfg, root := restoreFixture(t)

	var out, errOut bytes.Buffer
	if err := runList(cfg, &out, &errOut); err != nil {
		t.Fatalf("runList() error = %v", err)
	}
	if !strings.Contains(out.String(), "No backups found in") {
		t.Errorf("empty list output = %q, want no-backups message", out.String())
	}

	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		writeRestoreTree(t, target, "seed")
	}
	goID, err := CreateSnapshot(cfg, "install", "unknown")
	if err != nil {
		t.Fatal(err)
	}

	repository := createLocalSkillRepository(t, root)
	bashID := runBashInstall(t, cfg, root, repository)

	out.Reset()
	errOut.Reset()
	if err := runList(cfg, &out, &errOut); err != nil {
		t.Fatalf("runList() error = %v", err)
	}
	for _, id := range []string{goID, bashID} {
		if !strings.Contains(out.String(), id) {
			t.Errorf("list output missing snapshot %s:\n%s", id, out.String())
		}
	}
	if errOut.Len() != 0 {
		t.Errorf("list produced unexpected warnings: %q", errOut.String())
	}
}

func TestRunListSkipsUnsafeEntriesWithWarnings(t *testing.T) {
	cfg, _ := restoreFixture(t)
	if err := os.MkdirAll(cfg.BackupRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	unsafeDir := filepath.Join(cfg.BackupRoot, "not-a-snapshot-id")
	if err := os.MkdirAll(unsafeDir, 0o700); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	if err := runList(cfg, &out, &errOut); err != nil {
		t.Fatalf("runList() error = %v", err)
	}
	if !strings.Contains(out.String(), "No valid backups found") {
		t.Errorf("list output = %q, want no-valid-backups message", out.String())
	}
	if !strings.Contains(errOut.String(), "unsafe backup ID") {
		t.Errorf("list warnings = %q, want unsafe backup ID warning", errOut.String())
	}
}

func TestRunRestoreByIDWritesInstalledStateFromSnapshotCommit(t *testing.T) {
	cfg, _ := restoreFixture(t)
	targets := []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget}
	for _, target := range targets {
		writeRestoreTree(t, target, "before")
	}
	commit := strings.Repeat("a", 40)
	snapshotID, err := CreateSnapshot(cfg, "install", commit)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		writeRestoreTree(t, target, "current")
	}

	var out bytes.Buffer
	if err := runRestore(cfg, snapshotID, &out); err != nil {
		t.Fatalf("runRestore() error = %v", err)
	}
	for _, target := range targets {
		assertMarker(t, filepath.Join(target, "marker.txt"), "before\n")
	}
	if !strings.Contains(out.String(), "Restored snapshot: "+snapshotID) {
		t.Errorf("restore output = %q, want restored snapshot id", out.String())
	}

	data, err := os.ReadFile(cfg.StatePath)
	if err != nil {
		t.Fatalf("restore did not write installed state: %v", err)
	}
	if !strings.Contains(string(data), "source_commit="+commit) {
		t.Errorf("installed state = %q, want restored commit %s", data, commit)
	}
}

func TestRunRestoreRejectsInvalidIDWithoutMutation(t *testing.T) {
	cfg, _ := restoreFixture(t)
	targets := []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget}
	for _, target := range targets {
		writeRestoreTree(t, target, "unchanged")
	}
	before := make(map[string][32]byte, len(targets))
	for _, target := range targets {
		fingerprint, err := fingerprintTree(target)
		if err != nil {
			t.Fatal(err)
		}
		before[target] = fingerprint
	}

	var out bytes.Buffer
	if err := runRestore(cfg, "not-a-valid-id", &out); err == nil {
		t.Fatal("runRestore() accepted an invalid snapshot ID")
	}
	for _, target := range targets {
		got, err := fingerprintTree(target)
		if err != nil || got != before[target] {
			t.Errorf("invalid restore mutated %s: err=%v", target, err)
		}
	}
	if _, err := os.Lstat(cfg.StatePath); !os.IsNotExist(err) {
		t.Errorf("invalid restore wrote installed state")
	}
}

func TestRunInstallIsIdempotentAndForceReinstalls(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL = "file://" + repository
	cfg.Branch = "main"
	cfg.MinimumSkillCount = 1
	environment := commandEnvironment(cfg)
	targets := []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget}

	var out bytes.Buffer
	if err := runInstall(cfg, false, environment, &out); err != nil {
		t.Fatalf("first runInstall() error = %v\n%s", err, out.String())
	}
	firstSnapshots, err := os.ReadDir(cfg.BackupRoot)
	if err != nil {
		t.Fatal(err)
	}
	beforeState, err := os.ReadFile(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	before := make(map[string][32]byte, len(targets))
	for _, target := range targets {
		fingerprint, err := fingerprintTree(target)
		if err != nil {
			t.Fatal(err)
		}
		before[target] = fingerprint
	}

	out.Reset()
	if err := runInstall(cfg, false, environment, &out); err != nil {
		t.Fatalf("second runInstall() error = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Already up to date") {
		t.Errorf("second install output = %q, want an already-up-to-date message", out.String())
	}
	secondSnapshots, err := os.ReadDir(cfg.BackupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondSnapshots) != len(firstSnapshots) {
		t.Errorf("no-op install created a new snapshot: before=%d after=%d", len(firstSnapshots), len(secondSnapshots))
	}
	afterState, err := os.ReadFile(cfg.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(afterState) != string(beforeState) {
		t.Errorf("no-op install changed installed state:\nbefore=%s\nafter=%s", beforeState, afterState)
	}
	for _, target := range targets {
		fingerprint, err := fingerprintTree(target)
		if err != nil {
			t.Fatal(err)
		}
		if fingerprint != before[target] {
			t.Errorf("no-op install mutated target %s", target)
		}
	}

	out.Reset()
	if err := runInstall(cfg, true, environment, &out); err != nil {
		t.Fatalf("force runInstall() error = %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "Already up to date") {
		t.Errorf("force install output = %q, should not report already up to date", out.String())
	}
	forcedSnapshots, err := os.ReadDir(cfg.BackupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(forcedSnapshots) != len(firstSnapshots)+1 {
		t.Errorf("--force did not create a new snapshot: before=%d after=%d", len(firstSnapshots), len(forcedSnapshots))
	}
}

func TestRunInstallReinstallsWhenTargetsDriftAtSameCommit(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL = "file://" + repository
	cfg.Branch = "main"
	cfg.MinimumSkillCount = 1
	environment := commandEnvironment(cfg)

	var out bytes.Buffer
	if err := runInstall(cfg, false, environment, &out); err != nil {
		t.Fatalf("first runInstall() error = %v\n%s", err, out.String())
	}
	firstSnapshots, err := os.ReadDir(cfg.BackupRoot)
	if err != nil {
		t.Fatal(err)
	}

	// Hand-edit a live target without changing the recorded/source commit,
	// simulating manual drift (targets_match_source, install-skills.sh:678-683).
	driftFile := filepath.Join(cfg.OpenCodeTarget, "alpha", "SKILL.md")
	if err := os.WriteFile(driftFile, []byte("drifted\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	out.Reset()
	if err := runInstall(cfg, false, environment, &out); err != nil {
		t.Fatalf("drifted runInstall() error = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "manual drift") {
		t.Errorf("drifted install output = %q, want a manual-drift warning", out.String())
	}
	secondSnapshots, err := os.ReadDir(cfg.BackupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondSnapshots) != len(firstSnapshots)+1 {
		t.Errorf("drifted install did not reinstall: before=%d after=%d", len(firstSnapshots), len(secondSnapshots))
	}
	data, err := os.ReadFile(driftFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "drifted") {
		t.Errorf("reinstall did not restore the canonical tree: %s", data)
	}
}

func TestRunStatusReportsDriftWarningWhenTargetsEdited(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL = "file://" + repository
	cfg.Branch = "main"
	cfg.MinimumSkillCount = 1
	environment := commandEnvironment(cfg)

	var installOut bytes.Buffer
	if err := runInstall(cfg, false, environment, &installOut); err != nil {
		t.Fatalf("runInstall() error = %v\n%s", err, installOut.String())
	}

	driftFile := filepath.Join(cfg.OpenCodeTarget, "alpha", "SKILL.md")
	if err := os.WriteFile(driftFile, []byte("drifted\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := runStatus(cfg, environment, &out); err != nil {
		t.Fatalf("runStatus() error = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "manual drift") {
		t.Errorf("status output = %q, want a manual-drift warning", out.String())
	}
	if !strings.Contains(out.String(), "~1 modified") {
		t.Errorf("status output = %q, want a modified-skill summary", out.String())
	}
	if strings.Contains(out.String(), "Already up to date") {
		t.Errorf("status output = %q, should not report up to date while drifted", out.String())
	}
}

func TestRunStatusReportsSkillChangeSummaryWhenUpstreamAdvances(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL = "file://" + repository
	cfg.Branch = "main"
	cfg.MinimumSkillCount = 1
	environment := commandEnvironment(cfg)

	var installOut bytes.Buffer
	if err := runInstall(cfg, false, environment, &installOut); err != nil {
		t.Fatalf("runInstall() error = %v\n%s", err, installOut.String())
	}

	// Advance the upstream repository: add "gamma", remove "beta".
	if err := os.RemoveAll(filepath.Join(repository, "skills", "beta")); err != nil {
		t.Fatal(err)
	}
	gamma := filepath.Join(repository, "skills", "gamma")
	if err := os.MkdirAll(gamma, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := "---\nname: gamma\ndescription: Fixture skill\n---\n\n# gamma\n"
	if err := os.WriteFile(filepath.Join(gamma, "SKILL.md"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
	gitEnv := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(root, "home"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(root, "home", ".gitconfig"),
	}
	for _, args := range [][]string{
		{"-C", repository, "add", "-A", "skills"},
		{"-C", repository, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "advance"},
	} {
		command := exec.Command("git", args...)
		command.Env = gitEnv
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}

	var out bytes.Buffer
	if err := runStatus(cfg, environment, &out); err != nil {
		t.Fatalf("runStatus() error = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "A newer skill-pack version is available") {
		t.Errorf("status output = %q, want a newer-version message", out.String())
	}
	if !strings.Contains(out.String(), "+1 new") || !strings.Contains(out.String(), "-1 removed") {
		t.Errorf("status output = %q, want +1 new/-1 removed counts", out.String())
	}
	if !strings.Contains(out.String(), "+ gamma") {
		t.Errorf("status output = %q, want added skill gamma listed", out.String())
	}
	if !strings.Contains(out.String(), "- beta") {
		t.Errorf("status output = %q, want removed skill beta listed", out.String())
	}
}

// TestRunInstallHasNoSkillChangeSummaryOnFreshInstall proves the first
// install (no previously resolved installed state) behaves like Bash's
// "none (fresh install)" path (install-skills.sh:1448-1450): it never prints
// the pre-upgrade "Skill changes vs" summary, because that summary only
// applies when a previously installed version was resolved and differs from
// the source (install-skills.sh:1441-1447).
func TestRunInstallHasNoSkillChangeSummaryOnFreshInstall(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL = "file://" + repository
	cfg.Branch = "main"
	cfg.MinimumSkillCount = 1
	environment := commandEnvironment(cfg)

	var out bytes.Buffer
	if err := runInstall(cfg, false, environment, &out); err != nil {
		t.Fatalf("runInstall() error = %v\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "Skill changes vs") {
		t.Errorf("fresh install output = %q, should not print a pre-upgrade skill change summary", out.String())
	}
}

// TestRunInstallPrintsSkillChangeSummaryOnUpgrade proves runInstall mirrors
// run_install's pre-upgrade skill change summary (install-skills.sh:1441-1447,
// summarize_skill_changes at install-skills.sh:624-676): once a previously
// installed version is resolved and the upstream commit has advanced, install
// logs "Skill changes vs <reference>:" and the added/modified/removed diff
// against the current reference target before replacing it.
func TestRunInstallPrintsSkillChangeSummaryOnUpgrade(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL = "file://" + repository
	cfg.Branch = "main"
	cfg.MinimumSkillCount = 1
	environment := commandEnvironment(cfg)

	var firstOut bytes.Buffer
	if err := runInstall(cfg, false, environment, &firstOut); err != nil {
		t.Fatalf("first runInstall() error = %v\n%s", err, firstOut.String())
	}

	// Advance the upstream repository: add "gamma", remove "beta" (same
	// fixture shape as TestRunStatusReportsSkillChangeSummaryWhenUpstreamAdvances).
	if err := os.RemoveAll(filepath.Join(repository, "skills", "beta")); err != nil {
		t.Fatal(err)
	}
	gamma := filepath.Join(repository, "skills", "gamma")
	if err := os.MkdirAll(gamma, 0o750); err != nil {
		t.Fatal(err)
	}
	manifest := "---\nname: gamma\ndescription: Fixture skill\n---\n\n# gamma\n"
	if err := os.WriteFile(filepath.Join(gamma, "SKILL.md"), []byte(manifest), 0o640); err != nil {
		t.Fatal(err)
	}
	gitEnv := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(root, "home"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(root, "home", ".gitconfig"),
	}
	for _, args := range [][]string{
		{"-C", repository, "add", "-A", "skills"},
		{"-C", repository, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "advance"},
	} {
		command := exec.Command("git", args...)
		command.Env = gitEnv
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}

	var out bytes.Buffer
	if err := runInstall(cfg, false, environment, &out); err != nil {
		t.Fatalf("upgrade runInstall() error = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Skill changes vs "+cfg.OpenCodeTarget+":") {
		t.Errorf("upgrade install output = %q, want a pre-upgrade skill change summary header", out.String())
	}
	if !strings.Contains(out.String(), "+1 new") || !strings.Contains(out.String(), "-1 removed") {
		t.Errorf("upgrade install output = %q, want +1 new/-1 removed counts", out.String())
	}
	if !strings.Contains(out.String(), "+ gamma") {
		t.Errorf("upgrade install output = %q, want added skill gamma listed", out.String())
	}
	if !strings.Contains(out.String(), "- beta") {
		t.Errorf("upgrade install output = %q, want removed skill beta listed", out.String())
	}
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		if _, err := os.Stat(filepath.Join(target, "gamma", "SKILL.md")); err != nil {
			t.Errorf("upgrade install did not populate %s/gamma: %v", target, err)
		}
		if _, err := os.Stat(filepath.Join(target, "beta")); !os.IsNotExist(err) {
			t.Errorf("upgrade install did not remove %s/beta: err=%v", target, err)
		}
	}
}
