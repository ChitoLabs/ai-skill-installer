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
	if err := runStatus(cfg, &out); err != nil {
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
	if err := runStatus(cfg, &statusOut); err != nil {
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

	var out bytes.Buffer
	if err := runInstall(cfg, false, commandEnvironment(cfg), &out); !errors.Is(err, errInstallerLockContended) {
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
