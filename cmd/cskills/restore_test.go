package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func restoreFixture(t *testing.T) (Configuration, string) {
	t.Helper()
	root := t.TempDir()
	for _, parent := range []string{"opencode", "claude", "agy"} {
		if err := os.MkdirAll(filepath.Join(root, parent), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	environment := map[string]string{
		"HOME":                       filepath.Join(root, "home"),
		"OPENCODE_SKILLS_DIR":        filepath.Join(root, "opencode", "skills"),
		"CLAUDE_SKILLS_DIR":          filepath.Join(root, "claude", "skills"),
		"AGY_SKILLS_DIR":             filepath.Join(root, "agy", "skills"),
		"SKILL_BACKUP_DIR":           filepath.Join(root, "BKOld"),
		"SKILL_INSTALLER_LOCK_FILE":  filepath.Join(root, "installer.lock"),
		"SKILL_INSTALLER_STATE_FILE": filepath.Join(root, "installer.state"),
	}
	if err := os.MkdirAll(environment["HOME"], 0o700); err != nil {
		t.Fatal(err)
	}
	cfg, err := ResolveConfiguration(environment, root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg, root
}

func writeRestoreTree(t *testing.T, path, marker string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "fixture-skill"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "fixture-skill", "SKILL.md"), []byte(marker+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "marker.txt"), []byte(marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func bashInstallerPath(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve test source path")
	}
	return filepath.Join(filepath.Dir(source), "..", "..", "install-skills.sh")
}

func isolatedBashEnv(cfg Configuration, root string, extra ...string) []string {
	environment := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(root, "home"),
		"TMPDIR=" + root,
		"NO_COLOR=1",
		"SKILL_PACK_REPOSITORY_URL=file:///isolated/no-network",
		"SKILL_PACK_BRANCH=main",
		"SKILL_PACK_MIN_SKILL_COUNT=1",
		"OPENCODE_SKILLS_DIR=" + cfg.OpenCodeTarget,
		"CLAUDE_SKILLS_DIR=" + cfg.ClaudeTarget,
		"AGY_SKILLS_DIR=" + cfg.AgyTarget,
		"SKILL_BACKUP_DIR=" + cfg.BackupRoot,
		"SKILL_INSTALLER_LOCK_FILE=" + cfg.LockPath,
		"SKILL_INSTALLER_STATE_FILE=" + cfg.StatePath,
	}
	return append(environment, extra...)
}

func runIsolatedBash(t *testing.T, cfg Configuration, root string, extraEnv []string, args ...string) error {
	t.Helper()
	command := exec.Command("bash", append([]string{bashInstallerPath(t)}, args...)...)
	command.Env = isolatedBashEnv(cfg, root, extraEnv...)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("bash installer %v failed: %w\n%s", args, err, output)
	}
	return nil
}

func createLocalSkillRepository(t *testing.T, root string) string {
	t.Helper()
	repository := filepath.Join(root, "local-repository")
	for _, name := range []string{"alpha", "beta"} {
		skill := filepath.Join(repository, "skills", name)
		if err := os.MkdirAll(skill, 0o750); err != nil {
			t.Fatal(err)
		}
		manifest := fmt.Sprintf("---\nname: %s\ndescription: Fixture skill\n---\n\n# %s\n", name, name)
		if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(manifest), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "--quiet", "--initial-branch=main", repository}, {"-C", repository, "add", "skills"}, {"-C", repository, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "--quiet", "-m", "fixture"}} {
		command := exec.Command("git", args...)
		command.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + filepath.Join(root, "home"),
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_CONFIG_GLOBAL=" + filepath.Join(root, "home", ".gitconfig"),
		}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	return repository
}

func bashInstallEnvironment(cfg Configuration, root, repository string) []string {
	environment := isolatedBashEnv(cfg, root)
	for index, value := range environment {
		if strings.HasPrefix(value, "SKILL_PACK_REPOSITORY_URL=") {
			environment[index] = "SKILL_PACK_REPOSITORY_URL=file://" + repository
		}
	}
	return environment
}

func runBashInstall(t *testing.T, cfg Configuration, root, repository string) string {
	t.Helper()
	command := exec.Command("bash", bashInstallerPath(t), "install")
	command.Env = bashInstallEnvironment(cfg, root, repository)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("isolated Bash install failed: %v\n%s", err, output)
	}
	entries, err := os.ReadDir(cfg.BackupRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && snapshotIDPattern.MatchString(entry.Name()) {
			return entry.Name()
		}
	}
	t.Fatalf("Bash install produced no snapshot; output: %s", output)
	return ""
}

func TestBashRestoresGoCreatedV2Snapshot(t *testing.T) {
	cfg, root := restoreFixture(t)
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		writeRestoreTree(t, target, "go-snapshot")
	}
	snapshotID, err := CreateSnapshot(cfg, "install", "unknown")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		writeRestoreTree(t, target, "current")
	}
	if err := runIsolatedBash(t, cfg, root, nil, "restore", snapshotID); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		data, err := os.ReadFile(filepath.Join(target, "marker.txt"))
		if err != nil || string(data) != "go-snapshot\n" {
			t.Errorf("Bash did not restore %s: data=%q err=%v", target, data, err)
		}
	}
	assertSafetySnapshot(t, cfg)
}

func TestGoRestoresBashGeneratedV2Snapshot(t *testing.T) {
	cfg, root := restoreFixture(t)
	writeRestoreTree(t, cfg.OpenCodeTarget, "before-opencode")
	writeRestoreTree(t, cfg.ClaudeTarget, "before-claude")
	// AGY intentionally does not exist when Bash captures the v2 snapshot.
	repository := createLocalSkillRepository(t, root)
	snapshotID := runBashInstall(t, cfg, root, repository)
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		writeRestoreTree(t, target, "after-bash-snapshot")
	}
	if _, err := restoreSnapshot(cfg, snapshotID); err != nil {
		t.Fatal(err)
	}
	assertMarker(t, filepath.Join(cfg.OpenCodeTarget, "marker.txt"), "before-opencode\n")
	assertMarker(t, filepath.Join(cfg.ClaudeTarget, "marker.txt"), "before-claude\n")
	if _, err := os.Lstat(cfg.AgyTarget); !os.IsNotExist(err) {
		t.Fatalf("captured absent AGY target should be absent, stat err=%v", err)
	}
	assertSafetySnapshot(t, cfg)
}

func TestGoRestoresBashLegacyV1CaptureSemantics(t *testing.T) {
	cfg, root := restoreFixture(t)
	snapshotID := "20260831T120000Z-1234abcd"
	snapshotPath := filepath.Join(cfg.BackupRoot, snapshotID)
	if err := os.MkdirAll(snapshotPath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRestoreTree(t, filepath.Join(snapshotPath, "claude"), "legacy-claude")
	metadataPath := filepath.Join(snapshotPath, "metadata")
	metadataCommand := exec.Command("bash", "-c", `printf '%s\n' \
  'format_version=1' "id=$2" 'created_utc=2026-08-31T12:00:00Z' \
  'reason=install' 'source_commit=unknown' 'opencode_existed=0' 'claude_existed=1' \
  'opencode_skill_count=0' 'claude_skill_count=1' >"$1"`,
		"legacy-snapshot-fixture", metadataPath, snapshotID)
	metadataCommand.Env = isolatedBashEnv(cfg, root)
	if output, err := metadataCommand.CombinedOutput(); err != nil {
		t.Fatalf("create isolated Bash v1 fixture: %v\n%s", err, output)
	}
	// The Bash v1 fixture contract only captures OpenCode and Claude; AGY has no tree.
	if err := os.RemoveAll(cfg.OpenCodeTarget); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(cfg.ClaudeTarget); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.AgyTarget, 0o700); err != nil {
		t.Fatal(err)
	}
	writeRestoreTree(t, cfg.AgyTarget, "uncaptured-agy")
	if _, err := restoreSnapshot(cfg, snapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(cfg.OpenCodeTarget); !os.IsNotExist(err) {
		t.Fatalf("captured absent OpenCode target should be absent, stat err=%v", err)
	}
	assertMarker(t, filepath.Join(cfg.ClaudeTarget, "marker.txt"), "legacy-claude\n")
	assertMarker(t, filepath.Join(cfg.AgyTarget, "marker.txt"), "uncaptured-agy\n")
	assertSafetySnapshot(t, cfg)
}

func TestRestoreLockContentionDoesNotMutateSnapshotOrTargets(t *testing.T) {
	cfg, _ := restoreFixture(t)
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		writeRestoreTree(t, target, "before-restore")
	}
	snapshotID, err := CreateSnapshot(cfg, "install", "unknown")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		writeRestoreTree(t, target, "current")
	}

	before := make([][32]byte, 3)
	for index, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		before[index], err = fingerprintTree(target)
		if err != nil {
			t.Fatal(err)
		}
	}
	backupEntries, err := os.ReadDir(cfg.BackupRoot)
	if err != nil {
		t.Fatal(err)
	}
	snapshotFingerprint, err := fingerprintTree(filepath.Join(cfg.BackupRoot, snapshotID))
	if err != nil {
		t.Fatal(err)
	}

	lock, err := acquireInstallerLock(cfg.LockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if _, err := restoreSnapshot(cfg, snapshotID); !errors.Is(err, errInstallerLockContended) {
		t.Fatalf("restore error = %v, want installer lock contention", err)
	}

	for index, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		if got, err := fingerprintTree(target); err != nil || got != before[index] {
			t.Errorf("restore changed target %s while lock was held: fingerprint=%x err=%v", target, got, err)
		}
		entries, err := os.ReadDir(filepath.Dir(target))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".cskill-restore-stage-") || strings.HasPrefix(entry.Name(), ".cskill-stage-") {
				t.Errorf("restore created stage directory while lock was held: %s", filepath.Join(filepath.Dir(target), entry.Name()))
			}
		}
	}
	afterBackupEntries, err := os.ReadDir(cfg.BackupRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(afterBackupEntries) != len(backupEntries) {
		t.Fatalf("restore changed backup entries while lock was held: before=%v after=%v", entryNames(backupEntries), entryNames(afterBackupEntries))
	}
	for index := range backupEntries {
		if backupEntries[index].Name() != afterBackupEntries[index].Name() {
			t.Fatalf("restore changed backup entries while lock was held: before=%v after=%v", entryNames(backupEntries), entryNames(afterBackupEntries))
		}
	}
	if got, err := fingerprintTree(filepath.Join(cfg.BackupRoot, snapshotID)); err != nil || got != snapshotFingerprint {
		t.Fatalf("restore changed source snapshot while lock was held: fingerprint=%x err=%v", got, err)
	}
}

func TestInvalidLockPathDoesNotMutateTransactionOrRestoreState(t *testing.T) {
	tests := []struct {
		name string
		run  func(Configuration, []stagedTarget) error
	}{
		{
			name: "transaction without preparation",
			run: func(cfg Configuration, replacements []stagedTarget) error {
				_, err := runTransactionWithPreparation(cfg, replacements, "install", "unknown", transactionHooks{}, nil)
				return err
			},
		},
		{
			name: "restore with preparation",
			run: func(cfg Configuration, _ []stagedTarget) error {
				_, err := restoreSnapshotWithHooks(cfg, "20260831T120000Z-1234abcd", transactionHooks{})
				return err
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, _ := restoreFixture(t)
			for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
				writeRestoreTree(t, target, "unchanged")
			}
			before := make([][32]byte, 3)
			for index, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
				var err error
				before[index], err = fingerprintTree(target)
				if err != nil {
					t.Fatal(err)
				}
			}
			cfg.LockPath = filepath.Join(filepath.Dir(cfg.LockPath), ".codegraph", "installer.lock")

			replacements := []stagedTarget{
				{Target: cfg.OpenCodeTarget},
				{Target: cfg.ClaudeTarget},
				{Target: cfg.AgyTarget},
			}
			if err := test.run(cfg, replacements); err == nil {
				t.Fatal("operation accepted a lock path inside the protected .codegraph directory")
			}
			if _, err := os.Lstat(cfg.LockPath); !os.IsNotExist(err) {
				t.Errorf("invalid configuration created lock file: %v", err)
			}
			if _, err := os.Lstat(cfg.BackupRoot); !os.IsNotExist(err) {
				t.Errorf("invalid configuration created backup root: %v", err)
			}
			for index, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
				if got, err := fingerprintTree(target); err != nil || got != before[index] {
					t.Errorf("invalid configuration changed target %s: fingerprint=%x err=%v", target, got, err)
				}
			}
		})
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	return names
}

func TestGoRestoreFailureRetainsSafetySnapshotAndRollsBack(t *testing.T) {
	cfg, _ := restoreFixture(t)
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		writeRestoreTree(t, target, "before")
	}
	snapshotID, err := CreateSnapshot(cfg, "install", "unknown")
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		writeRestoreTree(t, target, "current")
	}
	before := make([][32]byte, 3)
	for i, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		before[i], err = fingerprintTree(target)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := restoreSnapshotWithHooks(cfg, snapshotID, transactionHooks{failAt: "after-install-0"}); err == nil || !strings.Contains(err.Error(), "injected transaction failure") {
		t.Fatalf("restore error = %v, want injected rollback failure", err)
	}
	for i, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		if got, err := fingerprintTree(target); err != nil || got != before[i] {
			t.Errorf("restore did not roll back %s: fingerprint=%x err=%v", target, got, err)
		}
	}
	assertSafetySnapshot(t, cfg)
}

func assertSafetySnapshot(t *testing.T, cfg Configuration) {
	t.Helper()
	entries, err := os.ReadDir(cfg.BackupRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() && snapshotIDPattern.MatchString(entry.Name()) {
			metadata, err := ValidateSnapshot(filepath.Join(cfg.BackupRoot, entry.Name()))
			if err == nil && metadata.Reason == "pre-restore" {
				return
			}
		}
	}
	t.Fatal("pre-restore safety snapshot was not retained")
}

func assertMarker(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != expected {
		t.Fatalf("marker at %s = %q, err=%v; want %q", path, data, err, expected)
	}
}
