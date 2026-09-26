package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// restoreSnapshot applies a validated snapshot through the internal transaction
// engine. It intentionally is not connected to the command-line dispatcher.
func restoreSnapshot(cfg Configuration, snapshotID string) (string, error) {
	return restoreSnapshotWithHooks(cfg, snapshotID, transactionHooks{})
}

func restoreSnapshotWithHooks(cfg Configuration, snapshotID string, hooks transactionHooks) (string, error) {
	if !snapshotIDPattern.MatchString(snapshotID) {
		return "", fmt.Errorf("invalid snapshot ID")
	}
	snapshotPath := filepath.Join(cfg.BackupRoot, snapshotID)
	targets := []struct {
		name string
		path string
	}{
		{"opencode", cfg.OpenCodeTarget},
		{"claude", cfg.ClaudeTarget},
		{"agy", cfg.AgyTarget},
	}
	for _, target := range targets {
		if pathsOverlap(snapshotPath, target.path) {
			return "", fmt.Errorf("restore snapshot overlaps configured target: %s", target.path)
		}
	}
	return runTransactionWithPreparation(cfg, nil, "pre-restore", "unknown", hooks, func() ([]stagedTarget, string, func(), error) {
		replacements := make([]stagedTarget, len(targets))
		for index, target := range targets {
			replacements[index] = stagedTarget{Target: target.path, Preserve: true}
		}
		// Snapshot validation and stage allocation intentionally remain after
		// lock acquisition; destination invariants were checked before locking.
		metadata, err := ValidateSnapshot(snapshotPath)
		if err != nil {
			return nil, "", nil, fmt.Errorf("validate restore snapshot: %w", err)
		}
		for index := range targets {
			switch index {
			case 0:
				replacements[index].Preserve = !metadata.OpenCode.Captured
				if metadata.OpenCode.Existed {
					replacements[index].StagedPath = filepath.Join(snapshotPath, targets[index].name)
				}
			case 1:
				replacements[index].Preserve = !metadata.Claude.Captured
				if metadata.Claude.Existed {
					replacements[index].StagedPath = filepath.Join(snapshotPath, targets[index].name)
				}
			case 2:
				replacements[index].Preserve = !metadata.AGY.Captured
				if metadata.AGY.Existed {
					replacements[index].StagedPath = filepath.Join(snapshotPath, targets[index].name)
				}
			}
		}
		var stagedPaths []string
		cleanup := func() {
			for _, path := range stagedPaths {
				_ = os.RemoveAll(path)
			}
		}
		for index, target := range targets {
			if replacements[index].Preserve || replacements[index].StagedPath == "" {
				continue
			}
			container, err := os.MkdirTemp(filepath.Dir(target.path), ".cskill-restore-stage-")
			if err != nil {
				return nil, "", cleanup, fmt.Errorf("stage %s restore: %w", target.name, err)
			}
			stagedPaths = append(stagedPaths, container)
			replacement := filepath.Join(container, "replacement")
			if err := copyTree(replacement, replacements[index].StagedPath); err != nil {
				return nil, "", cleanup, fmt.Errorf("stage %s snapshot tree: %w", target.name, err)
			}
			replacements[index].StagedPath = replacement
		}
		return replacements, metadata.SourceCommit, cleanup, nil
	}, nil)
}
