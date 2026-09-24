package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const stateCommit = "0123456789abcdef0123456789abcdef01234567"

func TestInstalledStateBashFixtureCompatibility(t *testing.T) {
	const bashFixture = "repository_url=https://example.test/skills?ref=a=b\nbranch=feature/x\nsource_commit=" + stateCommit + "\nskill_count=123\ninstalled_utc=2026-09-24T12:34:56Z\n"
	got, err := ParseInstalledState(strings.NewReader(bashFixture))
	if err != nil {
		t.Fatalf("ParseInstalledState() error = %v", err)
	}
	want := InstalledState{
		RepositoryURL: "https://example.test/skills?ref=a=b",
		Branch:        "feature/x",
		SourceCommit:  stateCommit,
		SkillCount:    "123",
		InstalledUTC:  "2026-09-24T12:34:56Z",
	}
	if got != want {
		t.Fatalf("ParseInstalledState() = %#v, want %#v", got, want)
	}
	serialized, err := SerializeInstalledState(got)
	if err != nil {
		t.Fatalf("SerializeInstalledState() error = %v", err)
	}
	if !bytes.Equal(serialized, []byte(bashFixture)) {
		t.Fatalf("serialized state bytes = %q, want Bash fixture bytes %q", serialized, bashFixture)
	}
}

func TestParseInstalledStateRejectsBashInvalidInputs(t *testing.T) {
	valid := "repository_url=https://example.test\nbranch=main\nsource_commit=" + stateCommit + "\n"
	tests := []struct {
		name  string
		input string
	}{
		{name: "duplicate key", input: valid + "branch=other\n"},
		{name: "unknown key", input: valid + "unexpected=value\n"},
		{name: "missing required key", input: "repository_url=https://example.test\nbranch=main\n"},
		{name: "empty required value", input: "repository_url=https://example.test\nbranch=\nsource_commit=" + stateCommit + "\n"},
		{name: "invalid commit", input: "repository_url=https://example.test\nbranch=main\nsource_commit=BAD\n"},
		{name: "blank line", input: valid + "\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseInstalledState(strings.NewReader(test.input)); err == nil {
				t.Fatal("ParseInstalledState() accepted invalid Bash state")
			}
		})
	}
}

func TestParseInstalledStateAllowsBashOptionalFieldsToBeAbsent(t *testing.T) {
	input := "repository_url=https://example.test\nbranch=main\nsource_commit=" + stateCommit + "\n"
	got, err := ParseInstalledState(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseInstalledState() error = %v", err)
	}
	if got.RepositoryURL != "https://example.test" || got.Branch != "main" || got.SourceCommit != stateCommit || got.SkillCount != "" || got.InstalledUTC != "" {
		t.Fatalf("ParseInstalledState() = %#v, want required Bash fields and empty optional values", got)
	}
}

func TestSerializeInstalledStateMatchesBashByteOrder(t *testing.T) {
	state := InstalledState{
		RepositoryURL: "https://example.test/skills",
		Branch:        "main",
		SourceCommit:  stateCommit,
		SkillCount:    "100",
		InstalledUTC:  "2026-09-24T12:34:56Z",
	}
	got, err := SerializeInstalledState(state)
	if err != nil {
		t.Fatalf("SerializeInstalledState() error = %v", err)
	}
	want := "repository_url=https://example.test/skills\nbranch=main\nsource_commit=" + stateCommit + "\nskill_count=100\ninstalled_utc=2026-09-24T12:34:56Z\n"
	if string(got) != want {
		t.Fatalf("serialized bytes = %q, want %q", got, want)
	}
}

func TestResolveInstalledStateUsesLatestValidInstallSnapshot(t *testing.T) {
	root := t.TempDir()
	backupRoot := filepath.Join(root, "backups")
	if err := os.Mkdir(backupRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	olderID := "20260924T100000Z-00000001"
	newerID := "20260924T110000Z-00000001"
	writeV1InstallSnapshot(t, backupRoot, olderID, stateCommit)
	writeV1InstallSnapshot(t, backupRoot, newerID, "abcdefabcdefabcdefabcdefabcdefabcdefabcd")
	// A lexically newer snapshot with invalid metadata must be skipped.
	invalidID := "20260924T120000Z-00000001"
	if err := os.Mkdir(filepath.Join(backupRoot, invalidID), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backupRoot, invalidID, "metadata"), []byte("reason=install\nsource_commit=bad\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, stateContent := range []string{"", "repository_url=x\nbranch=y\nsource_commit=invalid\n"} {
		statePath := filepath.Join(root, "state")
		if stateContent != "" {
			if err := os.WriteFile(statePath, []byte(stateContent), 0o600); err != nil {
				t.Fatal(err)
			}
		} else if err := os.RemoveAll(statePath); err != nil {
			t.Fatal(err)
		}
		got, err := ResolveInstalledState(statePath, backupRoot)
		if err != nil {
			t.Fatalf("ResolveInstalledState() error = %v", err)
		}
		if got.SourceCommit != "abcdefabcdefabcdefabcdefabcdefabcdefabcd" {
			t.Fatalf("fallback commit = %q, want newest valid install snapshot commit", got.SourceCommit)
		}
		if got.RepositoryURL != "" || got.Branch != "" {
			t.Fatalf("fallback unexpectedly populated state-only fields: %#v", got)
		}
	}
}

func TestResolveInstalledStatePrefersValidStateAndRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	backupRoot := filepath.Join(root, "backups")
	if err := os.Mkdir(backupRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state")
	validState := "repository_url=https://example.test\nbranch=main\nsource_commit=" + stateCommit + "\n"
	if err := os.WriteFile(statePath, []byte(validState), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveInstalledState(statePath, backupRoot)
	if err != nil || got.SourceCommit != stateCommit || got.RepositoryURL != "https://example.test" {
		t.Fatalf("ResolveInstalledState() = %#v, %v; valid state should win", got, err)
	}
	if err := os.Remove(statePath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "missing-state"), statePath); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveInstalledState(statePath, backupRoot); err == nil {
		t.Fatal("ResolveInstalledState() accepted a symlink without a snapshot fallback")
	}
}

func writeV1InstallSnapshot(t *testing.T, backupRoot, id, commit string) {
	t.Helper()
	path := filepath.Join(backupRoot, id)
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	fixture := "format_version=1\nid=" + id + "\ncreated_utc=2026-09-24T10:00:00Z\nreason=install\nsource_commit=" + commit + "\nopencode_existed=0\nclaude_existed=0\nopencode_skill_count=0\nclaude_skill_count=0\n"
	if err := os.WriteFile(filepath.Join(path, "metadata"), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
}
