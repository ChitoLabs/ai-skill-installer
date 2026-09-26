package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repositoryHeadCommit resolves the HEAD commit of an isolated local fixture
// repository built by createLocalSkillRepository, using the same isolated
// git environment (no user/system config, no network).
func repositoryHeadCommit(t *testing.T, root, repository string) string {
	t.Helper()
	command := exec.Command("git", "-C", repository, "rev-parse", "HEAD")
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + filepath.Join(root, "home"),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(root, "home", ".gitconfig"),
	}
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(output))
}

// TestGoReadsBashWrittenInstalledState proves the direct interoperability
// direction: a state file produced by write_installed_state through a real,
// isolated Bash install is read correctly by ResolveInstalledState.
func TestGoReadsBashWrittenInstalledState(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	if _, err := os.Lstat(cfg.StatePath); !os.IsNotExist(err) {
		t.Fatalf("fixture unexpectedly pre-created the state file: %v", err)
	}
	runBashInstall(t, cfg, root, repository)

	wantCommit := repositoryHeadCommit(t, root, repository)
	got, err := ResolveInstalledState(cfg.StatePath, cfg.BackupRoot)
	if err != nil {
		t.Fatalf("ResolveInstalledState() error = %v", err)
	}
	if got.SourceCommit != wantCommit {
		t.Errorf("SourceCommit = %q, want %q", got.SourceCommit, wantCommit)
	}
	if got.RepositoryURL != "file://"+repository {
		t.Errorf("RepositoryURL = %q, want %q", got.RepositoryURL, "file://"+repository)
	}
	if got.Branch != "main" {
		t.Errorf("Branch = %q, want %q", got.Branch, "main")
	}
	if got.SkillCount != "2" {
		t.Errorf("SkillCount = %q, want %q", got.SkillCount, "2")
	}
	if got.InstalledUTC == "" {
		t.Error("InstalledUTC was not recorded")
	}
}

// TestGoFallsBackToBashInstallSnapshotWhenStateFileMissing proves the
// missing-state fallback rule: once the state file Bash wrote is gone, Go
// falls back to the newest reason=install snapshot, exactly like
// resolve_installed_commit does through find_latest_install_commit.
func TestGoFallsBackToBashInstallSnapshotWhenStateFileMissing(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	runBashInstall(t, cfg, root, repository)
	wantCommit := repositoryHeadCommit(t, root, repository)

	if err := os.Remove(cfg.StatePath); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveInstalledState(cfg.StatePath, cfg.BackupRoot)
	if err != nil {
		t.Fatalf("ResolveInstalledState() error = %v", err)
	}
	if got.SourceCommit != wantCommit {
		t.Errorf("fallback SourceCommit = %q, want %q", got.SourceCommit, wantCommit)
	}
	if got.RepositoryURL != "" || got.Branch != "" {
		t.Errorf("fallback unexpectedly populated state-only fields: %#v", got)
	}
}

// TestGoFallsBackToBashInstallSnapshotWhenStateFileInvalid proves the
// invalid-state fallback rule using a state file Bash itself would reject:
// an unknown key alongside otherwise well-formed content. load_installed_state
// rejects any unrecognized key and returns 1, and ParseInstalledState must
// reject it the same way so ResolveInstalledState falls back identically.
func TestGoFallsBackToBashInstallSnapshotWhenStateFileInvalid(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	runBashInstall(t, cfg, root, repository)
	wantCommit := repositoryHeadCommit(t, root, repository)

	corrupted := "repository_url=" + "file://" + repository + "\nbranch=main\nsource_commit=" + wantCommit + "\nunexpected_key=1\n"
	if err := os.WriteFile(cfg.StatePath, []byte(corrupted), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveInstalledState(cfg.StatePath, cfg.BackupRoot)
	if err != nil {
		t.Fatalf("ResolveInstalledState() error = %v", err)
	}
	if got.SourceCommit != wantCommit {
		t.Errorf("fallback SourceCommit = %q, want %q", got.SourceCommit, wantCommit)
	}
}

// TestBashStatusAcceptsGoWrittenInstalledState proves the reverse direction:
// run_status (via load_installed_state/resolve_installed_commit) accepts a
// state file written by Go's WriteInstalledState without any Bash changes.
func TestBashStatusAcceptsGoWrittenInstalledState(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	commit := repositoryHeadCommit(t, root, repository)

	state := InstalledState{
		RepositoryURL: "file://" + repository,
		Branch:        "main",
		SourceCommit:  commit,
		SkillCount:    "2",
		InstalledUTC:  "2026-09-24T12:34:56Z",
	}
	if err := WriteInstalledState(cfg.StatePath, state); err != nil {
		t.Fatalf("WriteInstalledState() error = %v", err)
	}

	command := exec.Command("bash", bashInstallerPath(t), "status")
	command.Env = bashInstallEnvironment(cfg, root, repository)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Bash status rejected a Go-written installed state: %v\n%s", err, output)
	}
	text := string(output)
	if strings.Contains(text, "Installed version: none") {
		t.Fatalf("Bash status did not recognize the Go-written state file:\n%s", text)
	}
	if !strings.Contains(text, "Installed version: "+commit) {
		t.Fatalf("Bash status output missing recognized installed commit:\n%s", text)
	}
}

// TestWriteInstalledStateCreatesParentAndRoundTrips mirrors write_installed_state:
// it creates a missing parent directory and the written bytes are readable
// back through ParseInstalledState.
func TestWriteInstalledStateCreatesParentAndRoundTrips(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "nested", "installer.state")
	state := InstalledState{
		RepositoryURL: "https://example.test/skills",
		Branch:        "main",
		SourceCommit:  strings.Repeat("c", 40),
		SkillCount:    "7",
		InstalledUTC:  "2026-09-26T00:00:00Z",
	}
	if err := WriteInstalledState(statePath, state); err != nil {
		t.Fatalf("WriteInstalledState() error = %v", err)
	}
	file, err := os.Open(statePath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	got, err := ParseInstalledState(file)
	if err != nil {
		t.Fatalf("ParseInstalledState() error = %v", err)
	}
	if got != state {
		t.Fatalf("round-tripped state = %#v, want %#v", got, state)
	}
	entries, err := os.ReadDir(filepath.Dir(statePath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".installed-skills-state.") {
			t.Errorf("temporary state file was not cleaned up: %s", entry.Name())
		}
	}
}

// TestWriteInstalledStateRejectsSymlinkPath mirrors write_installed_state's
// own guard: "[[ ! -L \"$STATE_PATH\" ]] || fail ...".
func TestWriteInstalledStateRejectsSymlinkPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "elsewhere")
	if err := os.WriteFile(target, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "installer.state")
	if err := os.Symlink(target, statePath); err != nil {
		t.Fatal(err)
	}
	state := InstalledState{RepositoryURL: "https://example.test", Branch: "main", SourceCommit: strings.Repeat("d", 40)}
	if err := WriteInstalledState(statePath, state); err == nil {
		t.Fatal("WriteInstalledState() accepted a symlinked state path")
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "existing" {
		t.Fatalf("WriteInstalledState() modified the symlink target: data=%q, err=%v", data, err)
	}
}

// TestGoInstalledStateFallbackIsStricterThanBash documents a real behavioral
// divergence in the missing/invalid state fallback: find_latest_install_commit
// (install-skills.sh:546-571) only greps a snapshot's metadata for a literal
// "^reason=install$" line and a "source_commit=" value matching the commit
// pattern; it never runs the strict key/consistency checks load_metadata (and
// Go's ValidateSnapshot) enforce. Go's ResolveInstalledState reuses
// ValidateSnapshot for its fallback and is therefore strictly safer but not
// byte-for-byte compatible with Bash's looser fallback for a snapshot whose
// metadata is otherwise incomplete. This is reported as a known limitation
// rather than relaxed, since loosening Go's fallback would weaken its
// snapshot-consistency guarantee.
func TestGoInstalledStateFallbackIsStricterThanBash(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	fakeCommit := strings.Repeat("a", 40)

	snapshotID := "20260924T100000Z-00000001"
	snapshotPath := filepath.Join(cfg.BackupRoot, snapshotID)
	if err := os.MkdirAll(snapshotPath, 0o700); err != nil {
		t.Fatal(err)
	}
	// Minimal metadata: satisfies Bash's grep-based fallback check but is
	// missing every other key load_metadata (and ParseSnapshotMetadata)
	// require, so it fails full snapshot validation.
	minimalMetadata := "reason=install\nsource_commit=" + fakeCommit + "\n"
	if err := os.WriteFile(filepath.Join(snapshotPath, "metadata"), []byte(minimalMetadata), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := ResolveInstalledState(cfg.StatePath, cfg.BackupRoot); err == nil {
		t.Fatal("ResolveInstalledState() unexpectedly accepted a snapshot with incomplete metadata; divergence assumption is stale")
	}

	command := exec.Command("bash", bashInstallerPath(t), "status")
	command.Env = bashInstallEnvironment(cfg, root, repository)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Bash status failed against its own loose fallback fixture: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Installed version: "+fakeCommit) {
		t.Fatalf("Bash status did not use its documented loose fallback as expected:\n%s", output)
	}
}
