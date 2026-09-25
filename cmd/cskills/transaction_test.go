package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func transactionFixture(t *testing.T) (Configuration, []stagedTarget) {
	t.Helper()
	root := t.TempDir()
	parent := filepath.Join(root, "managed")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{
		"HOME":                       root,
		"OPENCODE_SKILLS_DIR":        filepath.Join(parent, "opencode", "skills"),
		"CLAUDE_SKILLS_DIR":          filepath.Join(parent, "claude", "skills"),
		"AGY_SKILLS_DIR":             filepath.Join(parent, "agy", "skills"),
		"SKILL_BACKUP_DIR":           filepath.Join(root, "backups"),
		"SKILL_INSTALLER_LOCK_FILE":  filepath.Join(root, "installer.lock"),
		"SKILL_INSTALLER_STATE_FILE": filepath.Join(root, "installer.state"),
	}
	cfg, err := ResolveConfiguration(environment, root)
	if err != nil {
		t.Fatal(err)
	}
	stages := make([]stagedTarget, 3)
	for i, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			t.Fatal(err)
		}
		stages[i] = stagedTarget{Target: target}
	}
	return cfg, stages
}

func makeTransactionTree(t *testing.T, root, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "skill", "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skill", "SKILL.md"), []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".hidden"), []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "skill", "link")
	if err := os.Symlink("SKILL.md", link); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestTransactionRollsBackAtEveryMutationBoundary(t *testing.T) {
	boundaries := []string{"after-snapshot", "after-stage-0", "after-stage-1", "after-stage-2", "after-quarantine-0", "after-quarantine-1", "after-quarantine-2", "after-install-0", "after-install-1", "after-install-2", "before-commit"}
	for _, boundary := range boundaries {
		t.Run(boundary, func(t *testing.T) {
			cfg, replacements := transactionFixture(t)
			originals := make([][32]byte, 3)
			for i := range replacements {
				if i == 1 { // A target may be absent and must remain absent.
					continue
				}
				makeTransactionTree(t, replacements[i].Target, "original")
				fingerprint, err := fingerprintTree(replacements[i].Target)
				if err != nil {
					t.Fatal(err)
				}
				originals[i] = fingerprint
			}
			for i := range replacements {
				stage := filepath.Join(t.TempDir(), "stage")
				makeTransactionTree(t, stage, "replacement")
				replacements[i].StagedPath = stage
			}

			id, err := runTransactionWithHooks(cfg, replacements, transactionHooks{failAt: boundary})
			if err == nil || !strings.Contains(err.Error(), "injected transaction failure") {
				t.Fatalf("runTransaction() error = %v; want injected error", err)
			}
			for i, replacement := range replacements {
				if i == 1 {
					if _, statErr := os.Lstat(replacement.Target); !os.IsNotExist(statErr) {
						t.Errorf("originally absent target was not absent after rollback: %v", statErr)
					}
					continue
				}
				got, fingerprintErr := fingerprintTree(replacement.Target)
				if fingerprintErr != nil || got != originals[i] {
					t.Errorf("target %s was not restored exactly: fingerprint=%x err=%v", replacement.Target, got, fingerprintErr)
				}
			}
			if id == "" {
				t.Fatal("failure did not return recovery snapshot ID")
			}
			if _, validateErr := ValidateSnapshot(filepath.Join(cfg.BackupRoot, id)); validateErr != nil {
				t.Fatalf("recovery snapshot not retained/valid: %v", validateErr)
			}
		})
	}
}

func TestTransactionCommitsAllTargetsAndRetainsSnapshot(t *testing.T) {
	cfg, replacements := transactionFixture(t)
	for i := range replacements {
		stage := filepath.Join(t.TempDir(), "stage")
		makeTransactionTree(t, stage, "replacement")
		replacements[i].StagedPath = stage
	}
	id, err := runTransaction(cfg, replacements)
	if err != nil {
		t.Fatalf("runTransaction() error = %v", err)
	}
	for _, replacement := range replacements {
		if err := verifyTransactionState(replacement.Target, mustFingerprint(t, replacement.StagedPath), true); err != nil {
			t.Errorf("committed target %s: %v", replacement.Target, err)
		}
	}
	if _, err := ValidateSnapshot(filepath.Join(cfg.BackupRoot, id)); err != nil {
		t.Fatalf("committed recovery snapshot invalid: %v", err)
	}
}

func TestTransactionCommitsAnAbsentDesiredTarget(t *testing.T) {
	cfg, replacements := transactionFixture(t)
	makeTransactionTree(t, replacements[0].Target, "remove")
	replacements[0].StagedPath = ""
	for i := 1; i < len(replacements); i++ {
		stage := filepath.Join(t.TempDir(), "stage")
		makeTransactionTree(t, stage, "replacement")
		replacements[i].StagedPath = stage
	}
	id, err := runTransaction(cfg, replacements)
	if err != nil {
		t.Fatalf("runTransaction() error = %v", err)
	}
	if _, err := os.Lstat(replacements[0].Target); !os.IsNotExist(err) {
		t.Fatalf("target requested absent still exists: %v", err)
	}
	metadata, err := ValidateSnapshot(filepath.Join(cfg.BackupRoot, id))
	if err != nil {
		t.Fatalf("recovery snapshot invalid: %v", err)
	}
	if !metadata.OpenCode.Existed {
		t.Fatal("snapshot lost the original target state")
	}
}

func TestTransactionLockContentionDoesNotTouchTargetsOrBackup(t *testing.T) {
	cfg, replacements := transactionFixture(t)
	makeTransactionTree(t, replacements[0].Target, "keep")
	before, err := fingerprintTree(replacements[0].Target)
	if err != nil {
		t.Fatal(err)
	}
	for i := range replacements {
		stage := filepath.Join(t.TempDir(), "stage")
		makeTransactionTree(t, stage, "new")
		replacements[i].StagedPath = stage
	}
	lock, err := acquireInstallerLock(cfg.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := runTransaction(cfg, replacements); !errors.Is(err, errInstallerLockContended) {
		t.Fatalf("runTransaction() error = %v; want lock contention", err)
	}
	after, err := fingerprintTree(replacements[0].Target)
	if err != nil || before != after {
		t.Fatalf("contended transaction changed target: before=%x after=%x err=%v", before, after, err)
	}
	if _, err := os.Lstat(cfg.BackupRoot); !os.IsNotExist(err) {
		t.Fatalf("lock contention changed backup root: %v", err)
	}
}

func TestTransactionReportsPostCommitCleanupFailureWithoutRollback(t *testing.T) {
	cfg, replacements := transactionFixture(t)
	for i := range replacements {
		makeTransactionTree(t, replacements[i].Target, "original")
		stage := filepath.Join(t.TempDir(), "stage")
		makeTransactionTree(t, stage, "replacement")
		replacements[i].StagedPath = stage
	}
	id, err := runTransactionWithHooks(cfg, replacements, transactionHooks{
		removeQuarantine: func(string) error { return errors.New("cleanup denied") },
	})
	if err == nil || !strings.Contains(err.Error(), "transaction committed") {
		t.Fatalf("runTransaction() error = %v; want committed cleanup warning", err)
	}
	for _, replacement := range replacements {
		data, readErr := os.ReadFile(filepath.Join(replacement.Target, "skill", "SKILL.md"))
		if readErr != nil || string(data) != "replacement" {
			t.Errorf("committed target was rolled back: data=%q err=%v", data, readErr)
		}
	}
	if _, err := ValidateSnapshot(filepath.Join(cfg.BackupRoot, id)); err != nil {
		t.Fatalf("recovery snapshot not retained: %v", err)
	}
}

func TestTransactionRejectsUnconfiguredAndUnsafeDestinationsBeforeMutation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Configuration, []stagedTarget)
	}{
		{name: "unconfigured target", mutate: func(_ *Configuration, targets []stagedTarget) {
			targets[0].Target = filepath.Join(t.TempDir(), "skills")
		}},
		{name: "root destination", mutate: func(cfg *Configuration, targets []stagedTarget) { cfg.OpenCodeTarget = "/"; targets[0].Target = "/" }},
		{name: "protected codegraph destination", mutate: func(cfg *Configuration, targets []stagedTarget) {
			cfg.OpenCodeTarget = filepath.Join(t.TempDir(), ".codegraph", "skills")
			targets[0].Target = cfg.OpenCodeTarget
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, replacements := transactionFixture(t)
			for i := range replacements {
				stage := filepath.Join(t.TempDir(), "stage")
				makeTransactionTree(t, stage, "new")
				replacements[i].StagedPath = stage
			}
			test.mutate(&cfg, replacements)
			if _, err := runTransaction(cfg, replacements); err == nil {
				t.Fatal("runTransaction() accepted unsafe input")
			}
			if _, err := os.Lstat(cfg.BackupRoot); !os.IsNotExist(err) {
				t.Errorf("invalid transaction created backup: %v", err)
			}
		})
	}
}

func mustFingerprint(t *testing.T, path string) [32]byte {
	t.Helper()
	fingerprint, err := fingerprintTree(path)
	if err != nil {
		t.Fatal(err)
	}
	return fingerprint
}
