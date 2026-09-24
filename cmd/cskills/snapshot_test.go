package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSnapshotID = "20260924T123456Z-deadbeef"

func validV2Metadata() SnapshotMetadata {
	return SnapshotMetadata{
		FormatVersion: 2,
		ID:            testSnapshotID,
		CreatedUTC:    "2026-09-24T12:34:56Z",
		Reason:        "install",
		SourceCommit:  strings.Repeat("a", 40),
		OpenCode:      SnapshotTarget{Captured: true, Existed: true, SkillCount: 1},
		Claude:        SnapshotTarget{Captured: true},
		AGY:           SnapshotTarget{Captured: true, Existed: true, SkillCount: 2},
	}
}

func TestSnapshotMetadataV1AndV2Compatibility(t *testing.T) {
	v1 := "format_version=1\nid=" + testSnapshotID + "\ncreated_utc=2026-09-24T12:34:56Z\nreason=pre-restore\nsource_commit=unknown\nopencode_existed=1\nclaude_existed=0\nopencode_skill_count=3\nclaude_skill_count=0\n"
	parsed, err := ParseSnapshotMetadata(strings.NewReader(v1))
	if err != nil {
		t.Fatalf("ParseSnapshotMetadata(v1) error = %v", err)
	}
	if !parsed.OpenCode.Captured || !parsed.Claude.Captured || parsed.AGY.Captured || parsed.AGY.Existed || parsed.AGY.SkillCount != 0 {
		t.Fatalf("v1 capture semantics = %+v", parsed)
	}

	want := validV2Metadata()
	want.AGY.SkillCount = 2
	encoded, err := SerializeSnapshotMetadata(want)
	if err != nil {
		t.Fatalf("SerializeSnapshotMetadata() error = %v", err)
	}
	wantBytes := []byte(fmt.Sprintf("format_version=2\nid=%s\ncreated_utc=2026-09-24T12:34:56Z\nreason=install\nsource_commit=%s\nopencode_captured=1\nclaude_captured=1\nagy_captured=1\nopencode_existed=1\nclaude_existed=0\nagy_existed=1\nopencode_skill_count=1\nclaude_skill_count=0\nagy_skill_count=2\n", testSnapshotID, strings.Repeat("a", 40)))
	if !bytes.Equal(encoded, wantBytes) {
		t.Fatalf("serialized metadata = %q, want %q", encoded, wantBytes)
	}
	got, err := ParseSnapshotMetadata(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("ParseSnapshotMetadata(v2) error = %v", err)
	}
	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

func TestParseSnapshotMetadataRejectsInvalidContracts(t *testing.T) {
	base, err := SerializeSnapshotMetadata(validV2Metadata())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "malformed line", input: string(base) + "broken\n", want: "malformed"},
		{name: "unknown key", input: string(base) + "extra=value\n", want: "unknown"},
		{name: "duplicate key", input: string(base) + "id=" + testSnapshotID + "\n", want: "duplicate"},
		{name: "missing required key", input: strings.Replace(string(base), "agy_skill_count=2\n", "", 1), want: "missing required"},
		{name: "invalid boolean", input: strings.Replace(string(base), "agy_captured=1", "agy_captured=true", 1), want: "invalid agy_captured"},
		{name: "negative count", input: strings.Replace(string(base), "agy_skill_count=2", "agy_skill_count=-1", 1), want: "invalid"},
		{name: "uncaptured target inconsistent", input: strings.Replace(string(base), "agy_captured=1", "agy_captured=0", 1), want: "uncaptured"},
		{name: "invalid identifier", input: strings.Replace(string(base), testSnapshotID, "../unsafe", 1), want: "invalid snapshot ID"},
		{name: "invalid timestamp shape", input: strings.Replace(string(base), "2026-09-24T12:34:56Z", "2026-09-24 12:34:56", 1), want: "invalid snapshot UTC date"},
		{name: "invalid source commit", input: strings.Replace(string(base), strings.Repeat("a", 40), "abc", 1), want: "invalid snapshot source commit"},
		{name: "v1 forbidden key", input: "format_version=1\nid=" + testSnapshotID + "\ncreated_utc=2026-09-24T12:34:56Z\nreason=install\nsource_commit=unknown\nopencode_existed=0\nclaude_existed=0\nopencode_skill_count=0\nclaude_skill_count=0\nagy_captured=0\n", want: "unsupported key"},
		{name: "unsupported version", input: strings.Replace(string(base), "format_version=2", "format_version=3", 1), want: "unsupported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseSnapshotMetadata(strings.NewReader(test.input))
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("ParseSnapshotMetadata() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestSerializeSnapshotMetadataRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name   string
		change func(*SnapshotMetadata)
	}{
		{name: "invalid ID", change: func(m *SnapshotMetadata) { m.ID = "unsafe" }},
		{name: "invalid reason", change: func(m *SnapshotMetadata) { m.Reason = "manual" }},
		{name: "negative count", change: func(m *SnapshotMetadata) { m.OpenCode.SkillCount = -1 }},
		{name: "bad capture consistency", change: func(m *SnapshotMetadata) { m.Claude.Captured = false; m.Claude.Existed = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := validV2Metadata()
			test.change(&metadata)
			if _, err := SerializeSnapshotMetadata(metadata); err == nil {
				t.Fatal("SerializeSnapshotMetadata() accepted invalid metadata")
			}
		})
	}
}

func TestValidateSnapshotFixtures(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, root string)
		want  string
	}{
		{name: "valid v2 including absent targets", setup: func(t *testing.T, root string) {
			metadata := validV2Metadata()
			metadata.AGY.SkillCount = 2
			writeSnapshotMetadata(t, root, metadata)
			makeSkill(t, filepath.Join(root, "opencode"), "one")
			makeSkill(t, filepath.Join(root, "agy"), "one")
			makeSkill(t, filepath.Join(root, "agy"), "two")
		}, want: ""},
		{name: "directory metadata ID mismatch", setup: func(t *testing.T, root string) {
			metadata := validV2Metadata()
			metadata.ID = "20260924T123456Z-cafebabe"
			writeSnapshotMetadata(t, root, metadata)
		}, want: "IDs do not match"},
		{name: "missing existing target tree", setup: func(t *testing.T, root string) { writeSnapshotMetadata(t, root, validV2Metadata()) }, want: "tree is missing or unsafe"},
		{name: "unexpected absent target tree", setup: func(t *testing.T, root string) {
			metadata := validV2Metadata()
			metadata.OpenCode.Existed = false
			metadata.OpenCode.SkillCount = 0
			writeSnapshotMetadata(t, root, metadata)
			if err := os.Mkdir(filepath.Join(root, "opencode"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, want: "unexpected opencode tree"},
		{name: "count mismatch", setup: func(t *testing.T, root string) {
			metadata := validV2Metadata()
			writeSnapshotMetadata(t, root, metadata)
			makeSkill(t, filepath.Join(root, "opencode"), "one")
			makeSkill(t, filepath.Join(root, "opencode"), "two")
			makeSkill(t, filepath.Join(root, "agy"), "one")
			makeSkill(t, filepath.Join(root, "agy"), "two")
		}, want: "count does not match"},
		{name: "protected codegraph tree", setup: func(t *testing.T, root string) {
			metadata := validV2Metadata()
			writeSnapshotMetadata(t, root, metadata)
			makeSkill(t, filepath.Join(root, "opencode"), "one")
			makeSkill(t, filepath.Join(root, "agy"), "one")
			makeSkill(t, filepath.Join(root, "agy"), "two")
			if err := os.MkdirAll(filepath.Join(root, "opencode", ".codegraph"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, want: ".codegraph"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), testSnapshotID)
			if err := os.Mkdir(root, 0o755); err != nil {
				t.Fatal(err)
			}
			test.setup(t, root)
			_, err := ValidateSnapshot(root)
			if test.want == "" {
				if err != nil {
					t.Fatalf("ValidateSnapshot() error = %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("ValidateSnapshot() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func writeSnapshotMetadata(t *testing.T, root string, metadata SnapshotMetadata) {
	t.Helper()
	data, err := SerializeSnapshotMetadata(metadata)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "metadata"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestValidateSnapshotRejectsSymlinkedTarget(t *testing.T) {
	root := filepath.Join(t.TempDir(), testSnapshotID)
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	metadata := validV2Metadata()
	writeSnapshotMetadata(t, root, metadata)
	target := filepath.Join(root, "opencode")
	if err := os.Symlink(t.TempDir(), target); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateSnapshot(root); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("ValidateSnapshot() error = %v, want unsafe target rejection", err)
	}
}

func TestParseSnapshotMetadataReadError(t *testing.T) {
	_, err := ParseSnapshotMetadata(errorReader{})
	if err == nil || !strings.Contains(err.Error(), "read snapshot metadata") {
		t.Fatalf("error = %v", err)
	}
}

func TestSnapshotMetadataBashByteSchemaAndV1AbsentTrees(t *testing.T) {
	metadata := SnapshotMetadata{
		FormatVersion: 2,
		ID:            testSnapshotID,
		CreatedUTC:    "2026-09-24T12:34:56Z",
		Reason:        "install",
		SourceCommit:  stateCommit,
		OpenCode:      SnapshotTarget{Captured: true, Existed: true, SkillCount: 1},
		Claude:        SnapshotTarget{Captured: true},
		AGY:           SnapshotTarget{Captured: true},
	}
	got, err := SerializeSnapshotMetadata(metadata)
	if err != nil {
		t.Fatalf("SerializeSnapshotMetadata() error = %v", err)
	}
	want := "format_version=2\n" +
		"id=" + testSnapshotID + "\n" +
		"created_utc=2026-09-24T12:34:56Z\nreason=install\nsource_commit=" + stateCommit + "\n" +
		"opencode_captured=1\nclaude_captured=1\nagy_captured=1\n" +
		"opencode_existed=1\nclaude_existed=0\nagy_existed=0\n" +
		"opencode_skill_count=1\nclaude_skill_count=0\nagy_skill_count=0\n"
	if string(got) != want {
		t.Fatalf("serialized metadata bytes = %q, want Bash create_snapshot bytes %q", got, want)
	}

	root := filepath.Join(t.TempDir(), testSnapshotID)
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := "format_version=1\nid=" + testSnapshotID + "\ncreated_utc=2026-09-24T12:34:56Z\nreason=install\nsource_commit=" + stateCommit + "\nopencode_existed=0\nclaude_existed=0\nopencode_skill_count=0\nclaude_skill_count=0\n"
	if err := os.WriteFile(filepath.Join(root, "metadata"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSnapshotMetadata(strings.NewReader(legacy))
	if err != nil {
		t.Fatalf("ParseSnapshotMetadata(v1) error = %v", err)
	}
	if !parsed.OpenCode.Captured || !parsed.Claude.Captured || parsed.AGY.Captured || parsed.AGY.Existed || parsed.AGY.SkillCount != 0 {
		t.Fatalf("v1 capture semantics = OpenCode:%#v Claude:%#v AGY:%#v", parsed.OpenCode, parsed.Claude, parsed.AGY)
	}
	if _, err := ValidateSnapshot(root); err != nil {
		t.Fatalf("ValidateSnapshot(v1 with absent trees) error = %v", err)
	}
}

func TestParseSnapshotMetadataRejectsDuplicateAndUnknownKeys(t *testing.T) {
	valid := "format_version=1\nid=" + testSnapshotID + "\ncreated_utc=2026-09-24T12:34:56Z\nreason=install\nsource_commit=" + stateCommit + "\nopencode_existed=0\nclaude_existed=0\nopencode_skill_count=0\nclaude_skill_count=0\n"
	tests := []struct {
		name  string
		input string
	}{
		{name: "duplicate key", input: valid + "reason=install\n"},
		{name: "unknown key", input: valid + "extra=value\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseSnapshotMetadata(strings.NewReader(test.input)); err == nil {
				t.Fatal("ParseSnapshotMetadata() accepted invalid Bash metadata")
			}
		})
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, fmt.Errorf("fixture read error") }
