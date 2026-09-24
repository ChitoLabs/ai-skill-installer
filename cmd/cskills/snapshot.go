package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

var (
	snapshotIDPattern   = regexp.MustCompile(`^[0-9]{8}T[0-9]{6}Z-[0-9a-f]{8}$`)
	snapshotTimePattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)
	commitPattern       = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// SnapshotTarget records whether a target was captured, existed, and how many
// direct-child SKILL.md manifests it contained.
type SnapshotTarget struct {
	Captured   bool
	Existed    bool
	SkillCount int
}

// SnapshotMetadata represents Bash snapshot metadata versions 1 and 2. Version
// 1 implicitly captures OpenCode and Claude only; version 2 records all targets.
type SnapshotMetadata struct {
	FormatVersion int
	ID            string
	CreatedUTC    string
	Reason        string
	SourceCommit  string
	OpenCode      SnapshotTarget
	Claude        SnapshotTarget
	AGY           SnapshotTarget
}

var metadataKeys = map[string]bool{
	"format_version": true, "id": true, "created_utc": true, "reason": true,
	"source_commit": true, "opencode_captured": true, "claude_captured": true,
	"agy_captured": true, "opencode_existed": true, "claude_existed": true,
	"agy_existed": true, "opencode_skill_count": true, "claude_skill_count": true,
	"agy_skill_count": true,
}

// ParseSnapshotMetadata parses the strict key/value contract used by the Bash
// installer. Unknown, duplicate, malformed, missing, and version-forbidden keys
// are rejected rather than silently ignored.
func ParseSnapshotMetadata(reader io.Reader) (SnapshotMetadata, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return SnapshotMetadata{}, fmt.Errorf("read snapshot metadata: %w", err)
	}
	values := make(map[string]string)
	for lineNumber, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		if line == "" {
			return SnapshotMetadata{}, fmt.Errorf("malformed snapshot metadata at line %d", lineNumber+1)
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return SnapshotMetadata{}, fmt.Errorf("malformed snapshot metadata at line %d", lineNumber+1)
		}
		if !metadataKeys[key] {
			return SnapshotMetadata{}, fmt.Errorf("unknown snapshot metadata key %q", key)
		}
		if _, exists := values[key]; exists {
			return SnapshotMetadata{}, fmt.Errorf("duplicate snapshot metadata key %q", key)
		}
		values[key] = value
	}
	if len(values) == 0 {
		return SnapshotMetadata{}, fmt.Errorf("snapshot metadata is empty")
	}
	version, err := requiredInt(values, "format_version")
	if err != nil || (version != 1 && version != 2) {
		return SnapshotMetadata{}, fmt.Errorf("unsupported or invalid snapshot metadata version %q", values["format_version"])
	}

	required := []string{"id", "created_utc", "reason", "source_commit", "opencode_existed", "claude_existed", "opencode_skill_count", "claude_skill_count"}
	if version == 2 {
		required = append(required, "opencode_captured", "claude_captured", "agy_captured", "agy_existed", "agy_skill_count")
	} else {
		for _, forbidden := range []string{"opencode_captured", "claude_captured", "agy_captured", "agy_existed", "agy_skill_count"} {
			if _, exists := values[forbidden]; exists {
				return SnapshotMetadata{}, fmt.Errorf("version 1 snapshot contains unsupported key %q", forbidden)
			}
		}
	}
	for _, key := range append([]string{"format_version"}, required...) {
		if _, exists := values[key]; !exists {
			return SnapshotMetadata{}, fmt.Errorf("missing required snapshot metadata key %q", key)
		}
	}
	metadata := SnapshotMetadata{FormatVersion: version, ID: values["id"], CreatedUTC: values["created_utc"], Reason: values["reason"], SourceCommit: values["source_commit"]}
	if version == 1 {
		metadata.OpenCode.Captured = true
		metadata.Claude.Captured = true
	} else {
		metadata.OpenCode.Captured, err = requiredBool(values, "opencode_captured")
		if err == nil {
			metadata.Claude.Captured, err = requiredBool(values, "claude_captured")
		}
		if err == nil {
			metadata.AGY.Captured, err = requiredBool(values, "agy_captured")
		}
		if err != nil {
			return SnapshotMetadata{}, err
		}
	}
	metadata.OpenCode.Existed, err = requiredBool(values, "opencode_existed")
	if err == nil {
		metadata.Claude.Existed, err = requiredBool(values, "claude_existed")
	}
	if err == nil {
		metadata.OpenCode.SkillCount, err = requiredInt(values, "opencode_skill_count")
	}
	if err == nil {
		metadata.Claude.SkillCount, err = requiredInt(values, "claude_skill_count")
	}
	if version == 2 && err == nil {
		metadata.AGY.Existed, err = requiredBool(values, "agy_existed")
	}
	if version == 2 && err == nil {
		metadata.AGY.SkillCount, err = requiredInt(values, "agy_skill_count")
	}
	if err != nil {
		return SnapshotMetadata{}, err
	}
	if err := validateSnapshotMetadata(metadata); err != nil {
		return SnapshotMetadata{}, err
	}
	return metadata, nil
}

// SerializeSnapshotMetadata emits current Bash-compatible v2 metadata in the
// same stable key order used by create_snapshot.
func SerializeSnapshotMetadata(metadata SnapshotMetadata) ([]byte, error) {
	metadata.FormatVersion = 2
	if err := validateSnapshotMetadata(metadata); err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "format_version=2\nid=%s\ncreated_utc=%s\nreason=%s\nsource_commit=%s\n", metadata.ID, metadata.CreatedUTC, metadata.Reason, metadata.SourceCommit)
	targets := []struct {
		name   string
		target SnapshotTarget
	}{{"opencode", metadata.OpenCode}, {"claude", metadata.Claude}, {"agy", metadata.AGY}}
	for _, item := range targets {
		fmt.Fprintf(&b, "%s_captured=%d\n", item.name, boolInt(item.target.Captured))
	}
	for _, item := range targets {
		fmt.Fprintf(&b, "%s_existed=%d\n", item.name, boolInt(item.target.Existed))
	}
	for _, item := range targets {
		fmt.Fprintf(&b, "%s_skill_count=%d\n", item.name, item.target.SkillCount)
	}
	return []byte(b.String()), nil
}

// ValidateSnapshot validates metadata against the snapshot directory contents.
func ValidateSnapshot(snapshotPath string) (SnapshotMetadata, error) {
	info, err := os.Lstat(snapshotPath)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return SnapshotMetadata{}, fmt.Errorf("snapshot directory is missing or unsafe: %s", snapshotPath)
	}
	metadataPath := filepath.Join(snapshotPath, "metadata")
	metadataInfo, err := os.Lstat(metadataPath)
	if err != nil || !metadataInfo.Mode().IsRegular() || metadataInfo.Mode()&os.ModeSymlink != 0 {
		return SnapshotMetadata{}, fmt.Errorf("snapshot metadata is missing or unsafe: %s", metadataPath)
	}
	file, err := os.Open(metadataPath)
	if err != nil {
		return SnapshotMetadata{}, fmt.Errorf("open snapshot metadata: %w", err)
	}
	defer file.Close()
	metadata, err := ParseSnapshotMetadata(file)
	if err != nil {
		return SnapshotMetadata{}, err
	}
	if metadata.ID != filepath.Base(snapshotPath) {
		return SnapshotMetadata{}, fmt.Errorf("snapshot directory and metadata IDs do not match")
	}
	for _, item := range []struct {
		name   string
		target SnapshotTarget
	}{
		{"opencode", metadata.OpenCode}, {"claude", metadata.Claude}, {"agy", metadata.AGY},
	} {
		tree := filepath.Join(snapshotPath, item.name)
		entry, statErr := os.Lstat(tree)
		if !item.target.Captured || !item.target.Existed {
			if statErr == nil || !os.IsNotExist(statErr) {
				return SnapshotMetadata{}, fmt.Errorf("unexpected %s tree for absent or uncaptured snapshot target", item.name)
			}
			continue
		}
		if statErr != nil || !entry.IsDir() || entry.Mode()&os.ModeSymlink != 0 {
			return SnapshotMetadata{}, fmt.Errorf("%s snapshot tree is missing or unsafe", item.name)
		}
		count, countErr := countSnapshotManifests(tree)
		if countErr != nil {
			return SnapshotMetadata{}, fmt.Errorf("validate %s snapshot tree: %w", item.name, countErr)
		}
		if count != item.target.SkillCount {
			return SnapshotMetadata{}, fmt.Errorf("%s snapshot skill count does not match metadata", item.name)
		}
	}
	return metadata, nil
}

func countSnapshotManifests(root string) (int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, err
	}
	count := 0
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path != root && entry.IsDir() && entry.Name() == ".codegraph" {
			return fmt.Errorf("protected .codegraph directory in snapshot")
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, entry := range entries {
		manifest := filepath.Join(root, entry.Name(), "SKILL.md")
		info, statErr := os.Lstat(manifest)
		if os.IsNotExist(statErr) || isNotDirectory(statErr) {
			continue
		}
		if statErr != nil {
			return 0, statErr
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("skill manifest is not a regular non-symlink file: %s", manifest)
		}
		count++
	}
	return count, nil
}

func validateSnapshotMetadata(metadata SnapshotMetadata) error {
	if !snapshotIDPattern.MatchString(metadata.ID) {
		return fmt.Errorf("invalid snapshot ID")
	}
	if !snapshotTimePattern.MatchString(metadata.CreatedUTC) {
		return fmt.Errorf("invalid snapshot UTC date")
	}
	if metadata.Reason != "install" && metadata.Reason != "pre-restore" {
		return fmt.Errorf("invalid snapshot reason")
	}
	if metadata.SourceCommit != "unknown" && !commitPattern.MatchString(metadata.SourceCommit) {
		return fmt.Errorf("invalid snapshot source commit")
	}
	if metadata.FormatVersion != 1 && metadata.FormatVersion != 2 {
		return fmt.Errorf("unsupported snapshot metadata version")
	}
	for _, item := range []struct {
		name   string
		target SnapshotTarget
	}{{"OpenCode", metadata.OpenCode}, {"Claude", metadata.Claude}, {"AGY", metadata.AGY}} {
		if item.target.SkillCount < 0 {
			return fmt.Errorf("invalid %s snapshot skill count", item.name)
		}
		if !item.target.Captured && (item.target.Existed || item.target.SkillCount != 0) {
			return fmt.Errorf("inconsistent %s metadata: uncaptured target must be absent with zero skills", item.name)
		}
		if !item.target.Existed && item.target.SkillCount != 0 {
			return fmt.Errorf("inconsistent %s metadata: absent target must have zero skills", item.name)
		}
	}
	if metadata.FormatVersion == 1 && (metadata.OpenCode.Captured != true || metadata.Claude.Captured != true || metadata.AGY.Captured || metadata.AGY.Existed || metadata.AGY.SkillCount != 0) {
		return fmt.Errorf("inconsistent version 1 capture semantics")
	}
	return nil
}

func requiredBool(values map[string]string, key string) (bool, error) {
	value, exists := values[key]
	if !exists || (value != "0" && value != "1") {
		return false, fmt.Errorf("invalid %s snapshot metadata", key)
	}
	return value == "1", nil
}

func requiredInt(values map[string]string, key string) (int, error) {
	value, exists := values[key]
	if !exists || value == "" {
		return 0, fmt.Errorf("missing or invalid snapshot metadata key %q", key)
	}
	for _, digit := range value {
		if digit < '0' || digit > '9' {
			return 0, fmt.Errorf("invalid snapshot metadata count %q", key)
		}
	}
	count, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid snapshot metadata count %q", key)
	}
	return count, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
