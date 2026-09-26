package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCreateSnapshotCapturesAllTargetsAndPreservesTree(t *testing.T) {
	root := t.TempDir()
	cfg := Configuration{
		OpenCodeTarget: filepath.Join(root, "opencode"),
		ClaudeTarget:   filepath.Join(root, "claude"),
		AgyTarget:      filepath.Join(root, "agy"),
		BackupRoot:     filepath.Join(root, "backups"),
	}
	for _, target := range []string{cfg.OpenCodeTarget, cfg.AgyTarget} {
		if err := os.MkdirAll(filepath.Join(target, "skill", "empty"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "skill", "SKILL.md"), []byte("---\nname: skill\n---\n"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, ".hidden"), []byte("dotfile"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	linkedFile := filepath.Join(root, "external.txt")
	if err := os.WriteFile(linkedFile, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(linkedFile, filepath.Join(cfg.OpenCodeTarget, "skill", "external-link")); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Date(2024, 3, 2, 1, 2, 3, 0, time.UTC)
	for _, target := range []string{cfg.OpenCodeTarget, cfg.AgyTarget} {
		if err := os.Chtimes(filepath.Join(target, ".hidden"), oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}

	id, err := CreateSnapshot(cfg, "install", strings.Repeat("b", 40))
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}
	if !snapshotIDPattern.MatchString(id) {
		t.Fatalf("snapshot ID = %q, want safe generated ID", id)
	}
	snapshotPath := filepath.Join(cfg.BackupRoot, id)
	metadata, err := ValidateSnapshot(snapshotPath)
	if err != nil {
		t.Fatalf("ValidateSnapshot() error = %v", err)
	}
	if !metadata.OpenCode.Captured || !metadata.Claude.Captured || !metadata.AGY.Captured ||
		!metadata.OpenCode.Existed || metadata.Claude.Existed || !metadata.AGY.Existed {
		t.Fatalf("captured/existed states = OpenCode:%+v Claude:%+v AGY:%+v", metadata.OpenCode, metadata.Claude, metadata.AGY)
	}
	for _, name := range []string{"opencode", "agy"} {
		copyRoot := filepath.Join(snapshotPath, name)
		if got, err := os.ReadFile(filepath.Join(copyRoot, ".hidden")); err != nil || string(got) != "dotfile" {
			t.Fatalf("copied %s dotfile = %q, %v", name, got, err)
		}
		info, err := os.Stat(filepath.Join(copyRoot, ".hidden"))
		if err != nil || info.Mode().Perm() != 0o600 || !info.ModTime().Equal(oldTime) {
			t.Fatalf("copied %s file metadata = %v, %v", name, info, err)
		}
		if info, err := os.Stat(filepath.Join(copyRoot, "skill", "empty")); err != nil || !info.IsDir() {
			t.Fatalf("copied %s empty directory = %v, %v", name, info, err)
		}
	}
	link := filepath.Join(snapshotPath, "opencode", "skill", "external-link")
	gotLink, err := os.Readlink(link)
	if err != nil || gotLink != linkedFile {
		t.Fatalf("copied symlink = %q, %v; want %q", gotLink, err, linkedFile)
	}
	if _, err := os.Lstat(filepath.Join(snapshotPath, "claude")); !os.IsNotExist(err) {
		t.Fatalf("absent Claude target was materialized: %v", err)
	}
}

func TestCreateSnapshotCanBeValidatedByBashList(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	targets := filepath.Join(home, "targets")
	if err := os.MkdirAll(targets, 0o700); err != nil {
		t.Fatal(err)
	}
	cfg := Configuration{
		OpenCodeTarget: filepath.Join(targets, "opencode", "skills"),
		ClaudeTarget:   filepath.Join(targets, "claude", "skills"),
		AgyTarget:      filepath.Join(targets, "agy", "skills"),
		BackupRoot:     filepath.Join(home, "backups"),
	}
	makeSkill(t, cfg.OpenCodeTarget, "open-skill")
	makeSkill(t, cfg.ClaudeTarget, "claude-skill")

	id, err := CreateSnapshot(cfg, "install", strings.Repeat("c", 40))
	if err != nil {
		t.Fatalf("CreateSnapshot() error = %v", err)
	}
	snapshotPath := filepath.Join(cfg.BackupRoot, id)
	metadata, err := ValidateSnapshot(snapshotPath)
	if err != nil {
		t.Fatalf("ValidateSnapshot() error = %v", err)
	}
	if !metadata.OpenCode.Captured || !metadata.OpenCode.Existed || metadata.OpenCode.SkillCount != 1 ||
		!metadata.Claude.Captured || !metadata.Claude.Existed || metadata.Claude.SkillCount != 1 ||
		!metadata.AGY.Captured || metadata.AGY.Existed || metadata.AGY.SkillCount != 0 {
		t.Fatalf("snapshot target semantics = OpenCode:%+v Claude:%+v AGY:%+v", metadata.OpenCode, metadata.Claude, metadata.AGY)
	}
	if _, err := os.Lstat(filepath.Join(snapshotPath, "agy")); !os.IsNotExist(err) {
		t.Fatalf("absent AGY target was materialized: %v", err)
	}

	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("Bash is unavailable: %v", err)
	}
	installer, err := filepath.Abs(filepath.Join("..", "..", "install-skills.sh"))
	if err != nil {
		t.Fatal(err)
	}
	environment := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"XDG_CONFIG_HOME=" + filepath.Join(home, "xdg"),
		"TMPDIR=" + filepath.Join(home, "tmp"),
		"LC_ALL=C",
		"NO_COLOR=1",
		"OPENCODE_SKILLS_DIR=" + cfg.OpenCodeTarget,
		"CLAUDE_SKILLS_DIR=" + cfg.ClaudeTarget,
		"AGY_SKILLS_DIR=" + cfg.AgyTarget,
		"SKILL_BACKUP_DIR=" + cfg.BackupRoot,
		"SKILL_INSTALLER_LOCK_FILE=" + filepath.Join(home, "installer.lock"),
		"SKILL_INSTALLER_STATE_FILE=" + filepath.Join(home, "installer.state"),
		"SKILL_INSTALLER_TEST_FAILPOINT=",
		"SKILL_PACK_REPOSITORY_URL=file:///network-disabled",
		"SKILL_PACK_BRANCH=main",
		"SKILL_PACK_MIN_SKILL_COUNT=1",
	}
	runBashList := func() (stdout, stderr string, err error) {
		t.Helper()
		cmd := exec.Command(bashPath, installer, "list")
		cmd.Env = environment
		var out, errOut bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errOut
		err = cmd.Run()
		return out.String(), errOut.String(), err
	}

	stdout, stderr, err := runBashList()
	if err != nil {
		t.Fatalf("Bash list failed: %v\nstdout:\n%s\nstderr:\n%s", err, stdout, stderr)
	}
	for _, expected := range []string{
		id,
		"OpenCode yes, Claude yes, AGY no",
		"Skills: 1/1/0",
		"Source commit: " + strings.Repeat("c", 40),
	} {
		if !strings.Contains(stdout, expected) {
			t.Errorf("Bash list output missing %q:\n%s", expected, stdout)
		}
	}
	if stderr != "" {
		t.Errorf("Bash list wrote unexpected stderr: %s", stderr)
	}

	metadataPath := filepath.Join(snapshotPath, "metadata")
	metadataBytes, err := os.ReadFile(metadataPath)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := bytes.Replace(metadataBytes, []byte("opencode_skill_count=1\n"), []byte("opencode_skill_count=2\n"), 1)
	if bytes.Equal(corrupt, metadataBytes) {
		t.Fatal("failed to construct invalid Bash validation fixture")
	}
	if err := os.WriteFile(metadataPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err = runBashList()
	if err == nil {
		t.Fatalf("Bash list accepted inconsistent Go snapshot metadata; stdout:\n%s\nstderr:\n%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "OpenCode snapshot skill count does not match metadata") {
		t.Fatalf("Bash validation failure was not preserved on stderr: %s", stderr)
	}
	for _, path := range []string{filepath.Join(home, "installer.lock"), filepath.Join(home, "installer.state")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("read-only Bash list touched %s: %v", path, err)
		}
	}
}

func TestCreateSnapshotCleansTemporaryDirectoryOnCopyError(t *testing.T) {
	root := t.TempDir()
	cfg := Configuration{
		OpenCodeTarget: filepath.Join(root, "opencode"),
		ClaudeTarget:   filepath.Join(root, "claude"),
		AgyTarget:      filepath.Join(root, "agy"),
		BackupRoot:     filepath.Join(root, "backups"),
	}
	if err := os.MkdirAll(filepath.Join(cfg.OpenCodeTarget, "real-skill"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfg.OpenCodeTarget, "real-skill", "SKILL.md"), []byte("manifest"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(cfg.OpenCodeTarget, "unsupported-pipe"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := CreateSnapshot(cfg, "install", "unknown"); err == nil {
		t.Fatal("CreateSnapshot() accepted a target containing an unsupported special file")
	}
	entries, err := os.ReadDir(cfg.BackupRoot)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("read backup root: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("pre-publish failure left backup entries: %v", entries)
	}
}

func TestCreateSnapshotRejectsInvalidInputsBeforeCreatingBackupRoot(t *testing.T) {
	root := t.TempDir()
	cfg := Configuration{
		OpenCodeTarget: filepath.Join(root, "opencode"),
		ClaudeTarget:   filepath.Join(root, "claude"),
		AgyTarget:      filepath.Join(root, "agy"),
		BackupRoot:     filepath.Join(root, "backups"),
	}
	if _, err := CreateSnapshot(cfg, "invalid", "unknown"); err == nil {
		t.Fatal("CreateSnapshot() accepted an invalid reason")
	}
	if _, err := os.Lstat(cfg.BackupRoot); !os.IsNotExist(err) {
		t.Fatalf("invalid input created backup root: %v", err)
	}
}

func TestCreateSnapshotRemovesAttemptBeforeRetryAndDoesNotReplaceConcurrentPublication(t *testing.T) {
	root := t.TempDir()
	cfg := snapshotTestConfiguration(root)
	makeSkill(t, cfg.OpenCodeTarget, "sample")
	ids := []string{"20260924T120000Z-00000001", "20260924T120000Z-00000002"}
	idIndex := 0
	firstPublication := filepath.Join(cfg.BackupRoot, ids[0])
	createConcurrentPath := true
	hooks := snapshotCreateHooks{
		newID: func(time.Time) (string, error) {
			id := ids[idIndex]
			idIndex++
			return id, nil
		},
		beforePublish: func(finalPath string) error {
			if finalPath == firstPublication && createConcurrentPath {
				createConcurrentPath = false
				if err := os.Mkdir(finalPath, 0o700); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(finalPath, "owner"), []byte("concurrent creator"), 0o600)
			}
			entries, err := os.ReadDir(cfg.BackupRoot)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if strings.HasPrefix(entry.Name(), ".snapshot."+ids[0]+".") {
					return fmt.Errorf("first attempt temporary directory remains before retry: %s", entry.Name())
				}
			}
			return nil
		},
	}

	id, err := createSnapshot(cfg, "install", "unknown", hooks)
	if err != nil {
		t.Fatalf("createSnapshot() error = %v", err)
	}
	if id != ids[1] {
		t.Fatalf("snapshot ID = %q, want retry ID %q", id, ids[1])
	}
	owner, err := os.ReadFile(filepath.Join(firstPublication, "owner"))
	if err != nil || string(owner) != "concurrent creator" {
		t.Fatalf("concurrent publication was replaced: owner=%q, err=%v", owner, err)
	}
	if _, err := ValidateSnapshot(filepath.Join(cfg.BackupRoot, id)); err != nil {
		t.Fatalf("retry snapshot is invalid: %v", err)
	}
}

func TestCreateSnapshotFailsClosedWhenSourceChangesDuringCapture(t *testing.T) {
	tests := []struct {
		name   string
		change func(string) error
	}{
		{name: "file bytes", change: func(path string) error {
			return os.WriteFile(filepath.Join(path, "sample", "SKILL.md"), []byte("changed bytes"), 0o600)
		}},
		{name: "file mode", change: func(path string) error {
			return os.Chmod(filepath.Join(path, "sample", "SKILL.md"), 0o640)
		}},
		{name: "entry name", change: func(path string) error {
			return os.Rename(filepath.Join(path, "sample", "SKILL.md"), filepath.Join(path, "sample", "RENAMED.md"))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			cfg := snapshotTestConfiguration(root)
			makeSkill(t, cfg.OpenCodeTarget, "sample")
			hooks := snapshotCreateHooks{afterCopy: func() error { return test.change(cfg.OpenCodeTarget) }}
			if _, err := createSnapshot(cfg, "install", "unknown", hooks); err == nil || !strings.Contains(err.Error(), "changed during snapshot capture") {
				t.Fatalf("createSnapshot() error = %v, want source-change rejection", err)
			}
			entries, err := os.ReadDir(cfg.BackupRoot)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("failed source verification left backup entries: %v", entries)
			}
		})
	}
}

func snapshotTestConfiguration(root string) Configuration {
	return Configuration{
		OpenCodeTarget: filepath.Join(root, "opencode"),
		ClaudeTarget:   filepath.Join(root, "claude"),
		AgyTarget:      filepath.Join(root, "agy"),
		BackupRoot:     filepath.Join(root, "backups"),
	}
}
