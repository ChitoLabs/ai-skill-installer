package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// stagedTarget describes one configured target and its already-prepared
// replacement. A nil StagedPath requests that the target be absent.
type stagedTarget struct {
	Target     string
	StagedPath string
	Preserve   bool
}

type transactionHooks struct {
	failAt           string
	removeQuarantine func(string) error
}

// runTransaction replaces exactly the three destinations in cfg. It is an
// internal primitive; it does not enable any public install or restore command.
func runTransaction(cfg Configuration, replacements []stagedTarget) (string, error) {
	return runTransactionWithHooks(cfg, replacements, transactionHooks{})
}

func runTransactionWithHooks(cfg Configuration, replacements []stagedTarget, hooks transactionHooks) (string, error) {
	return runTransactionWithOptions(cfg, replacements, "install", "unknown", hooks)
}

func runTransactionWithOptions(cfg Configuration, replacements []stagedTarget, snapshotReason, sourceCommit string, hooks transactionHooks) (string, error) {
	return runTransactionWithPreparation(cfg, replacements, snapshotReason, sourceCommit, hooks, nil)
}

// runTransactionWithPreparation validates configuration and destination paths
// before creating the shared installer lock, then revalidates and holds the
// lock through preparation and commit/rollback. Restore supplies a preparation
// callback so snapshot validation and staging happen only while locked.
func runTransactionWithPreparation(cfg Configuration, replacements []stagedTarget, snapshotReason, sourceCommit string, hooks transactionHooks, prepare func() ([]stagedTarget, string, func(), error)) (string, error) {
	if prepare != nil {
		// The final replacements depend on snapshot metadata. Validate the
		// configured destinations first using all-preserve placeholders; the
		// complete replacements are validated again under the lock.
		replacements = []stagedTarget{
			{Target: cfg.OpenCodeTarget, Preserve: true},
			{Target: cfg.ClaudeTarget, Preserve: true},
			{Target: cfg.AgyTarget, Preserve: true},
		}
	}
	if err := validateTransactionInputs(cfg, replacements); err != nil {
		return "", err
	}

	lock, err := acquireInstallerLock(cfg.LockPath)
	if err != nil {
		return "", err
	}
	defer lock.Close()

	if prepare != nil {
		var cleanup func()
		replacements, sourceCommit, cleanup, err = prepare()
		if err != nil {
			if cleanup != nil {
				cleanup()
			}
			return "", err
		}
		if cleanup != nil {
			defer cleanup()
		}
	}
	if err := validateTransactionInputs(cfg, replacements); err != nil {
		return "", err
	}

	id, err := createSnapshot(cfg, snapshotReason, sourceCommit, snapshotCreateHooks{})
	if err != nil {
		return "", fmt.Errorf("create transaction recovery snapshot: %w", err)
	}
	if err := transactionFail(hooks, "after-snapshot"); err != nil {
		return id, err
	}

	type targetState struct {
		path, staged, quarantine         string
		stageContainer                   string
		expectedFingerprint              [32]byte
		originalExists, moved, installed bool
	}
	states := make([]targetState, len(replacements))
	cleanupStages := func() error {
		var result error
		for _, state := range states {
			if state.staged != "" {
				result = errors.Join(result, os.RemoveAll(filepath.Dir(state.staged)))
			}
		}
		return result
	}
	defer func() { _ = cleanupStages() }()

	for i, replacement := range replacements {
		states[i].path = replacement.Target
		if replacement.Preserve || replacement.StagedPath == "" {
			continue
		}
		parent := filepath.Dir(replacement.Target)
		container, err := os.MkdirTemp(parent, ".cskill-stage-")
		if err != nil {
			return id, fmt.Errorf("stage replacement for %s: %w", replacement.Target, err)
		}
		states[i].stageContainer = container
		states[i].staged = filepath.Join(container, "replacement")
		if err := copyTree(states[i].staged, replacement.StagedPath); err != nil {
			return id, fmt.Errorf("copy staged replacement for %s: %w", replacement.Target, err)
		}
		fingerprint, err := fingerprintTree(states[i].staged)
		if err != nil {
			return id, fmt.Errorf("verify staged replacement for %s: %w", replacement.Target, err)
		}
		states[i].expectedFingerprint = fingerprint
		if err := transactionFail(hooks, fmt.Sprintf("after-stage-%d", i)); err != nil {
			return id, err
		}
	}

	for i := range states {
		state := &states[i]
		if replacements[i].Preserve {
			continue
		}
		state.quarantine = state.path + ".transaction." + id
		if _, err := os.Lstat(state.quarantine); err == nil {
			return id, fmt.Errorf("transaction quarantine already exists: %s", state.quarantine)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return id, fmt.Errorf("inspect transaction quarantine %s: %w", state.quarantine, err)
		}
		_, err := os.Lstat(state.path)
		if err == nil {
			state.originalExists = true
		} else if !errors.Is(err, fs.ErrNotExist) {
			return id, fmt.Errorf("inspect transaction target %s: %w", state.path, err)
		}
	}

	rollback := func(cause error) (string, error) {
		var rollbackErr error
		for i := len(states) - 1; i >= 0; i-- {
			state := &states[i]
			if replacements[i].Preserve {
				continue
			}
			if state.installed {
				if err := os.RemoveAll(state.path); err != nil {
					rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove failed target %s: %w", state.path, err))
				}
			}
			if state.moved {
				if err := os.Rename(state.quarantine, state.path); err != nil {
					rollbackErr = errors.Join(rollbackErr, fmt.Errorf("restore quarantined original %s: %w", state.path, err))
				}
			} else if !state.originalExists {
				if _, err := os.Lstat(state.path); err == nil {
					if removeErr := os.RemoveAll(state.path); removeErr != nil {
						rollbackErr = errors.Join(rollbackErr, fmt.Errorf("remove originally absent target %s: %w", state.path, removeErr))
					}
				} else if !errors.Is(err, fs.ErrNotExist) {
					rollbackErr = errors.Join(rollbackErr, err)
				}
			}
		}
		if rollbackErr != nil {
			return id, errors.Join(cause, fmt.Errorf("rollback incomplete; retain recovery snapshot %s: %w", id, rollbackErr))
		}
		return id, cause
	}

	for i := range states {
		state := &states[i]
		if replacements[i].Preserve {
			continue
		}
		if state.originalExists {
			if err := os.Rename(state.path, state.quarantine); err != nil {
				return rollback(fmt.Errorf("quarantine original %s: %w", state.path, err))
			}
			state.moved = true
		}
		if err := transactionFail(hooks, fmt.Sprintf("after-quarantine-%d", i)); err != nil {
			return rollback(err)
		}
	}
	for i := range states {
		state := &states[i]
		if replacements[i].Preserve {
			continue
		}
		if state.staged != "" {
			if err := os.Rename(state.staged, state.path); err != nil {
				return rollback(fmt.Errorf("install staged target %s: %w", state.path, err))
			}
			state.installed = true
		}
		if err := transactionFail(hooks, fmt.Sprintf("after-install-%d", i)); err != nil {
			return rollback(err)
		}
	}
	for i, replacement := range replacements {
		if replacement.Preserve {
			continue
		}
		if err := verifyTransactionState(replacement.Target, states[i].expectedFingerprint, replacement.StagedPath != ""); err != nil {
			return rollback(fmt.Errorf("verify committed target %s: %w", replacement.Target, err))
		}
	}
	if err := transactionFail(hooks, "before-commit"); err != nil {
		return rollback(err)
	}

	var cleanupErr error
	for _, state := range states {
		if state.stageContainer != "" {
			if err := os.RemoveAll(state.stageContainer); err != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("committed; could not clean staging path %s: %w", state.stageContainer, err))
			}
		}
		if !state.moved {
			continue
		}
		remove := hooks.removeQuarantine
		if remove == nil {
			remove = os.RemoveAll
		}
		if err := remove(state.quarantine); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("committed; could not clean quarantine %s: %w", state.quarantine, err))
		}
	}
	if err := transactionFail(hooks, "post-commit-cleanup"); err != nil {
		cleanupErr = errors.Join(cleanupErr, err)
	}
	if cleanupErr != nil {
		return id, fmt.Errorf("transaction committed; recovery snapshot %s retained: %w", id, cleanupErr)
	}
	return id, nil
}

func transactionFail(hooks transactionHooks, boundary string) error {
	if hooks.failAt == boundary {
		return fmt.Errorf("injected transaction failure at %s", boundary)
	}
	return nil
}

func verifyTransactionState(target string, expectedFingerprint [32]byte, shouldExist bool) error {
	info, err := os.Lstat(target)
	if !shouldExist {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("target should be absent")
	}
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		if err != nil {
			return fmt.Errorf("target is missing or unsafe: %w", err)
		}
		return fmt.Errorf("target is missing or unsafe")
	}
	got, err := fingerprintTree(target)
	if err != nil {
		return err
	}
	if got != expectedFingerprint {
		return fmt.Errorf("target does not match staged replacement")
	}
	return nil
}

func validateTransactionInputs(cfg Configuration, replacements []stagedTarget) error {
	if !cfg.validated {
		return fmt.Errorf("transaction requires a validated configuration")
	}
	expected := []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget}
	if len(replacements) != len(expected) {
		return fmt.Errorf("transaction requires exactly three configured targets")
	}
	paths := append([]string(nil), expected...)
	paths = append(paths, cfg.BackupRoot, cfg.LockPath, cfg.StatePath)
	for _, path := range paths {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path || path == string(filepath.Separator) {
			return fmt.Errorf("transaction path is not a safe absolute path: %q", path)
		}
		resolved, err := canonicalMissingPath(path)
		if err != nil || resolved != path {
			return fmt.Errorf("transaction path traverses a symbolic link or cannot be resolved: %s", path)
		}
		for _, part := range strings.Split(filepath.Clean(path), string(filepath.Separator)) {
			if part == ".codegraph" {
				return fmt.Errorf("transaction path is inside protected .codegraph: %s", path)
			}
		}
	}
	for i, target := range expected {
		if filepath.Base(target) != "skills" {
			return fmt.Errorf("transaction target must be a configured skills directory: %s", target)
		}
		for j := 0; j < i; j++ {
			if pathsOverlap(target, expected[j]) {
				return fmt.Errorf("configured transaction targets overlap")
			}
		}
		if pathsOverlap(target, cfg.BackupRoot) || pathsOverlap(target, cfg.LockPath) || pathsOverlap(target, cfg.StatePath) {
			return fmt.Errorf("configured transaction paths overlap")
		}
		parentInfo, err := os.Lstat(filepath.Dir(target))
		if err != nil || !parentInfo.IsDir() || parentInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("target parent is missing or unsafe: %s", filepath.Dir(target))
		}
		if replacements[i].Target != target {
			return fmt.Errorf("replacement %d does not match its configured target", i)
		}
		if replacements[i].Preserve {
			if replacements[i].StagedPath != "" {
				return fmt.Errorf("preserved target %d must not have a staged replacement", i)
			}
			continue
		}
		if replacements[i].StagedPath != "" {
			stageInfo, err := os.Lstat(replacements[i].StagedPath)
			if err != nil || !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("staged replacement is missing or unsafe: %s", replacements[i].StagedPath)
			}
			if pathsOverlap(replacements[i].StagedPath, target) || pathsOverlap(replacements[i].StagedPath, cfg.BackupRoot) {
				return fmt.Errorf("staged replacement overlaps managed paths: %s", replacements[i].StagedPath)
			}
			if err := rejectCodegraphPath(replacements[i].StagedPath, "staged replacement"); err != nil {
				return err
			}
		}
	}
	if pathsOverlap(cfg.BackupRoot, cfg.LockPath) || pathsOverlap(cfg.BackupRoot, cfg.StatePath) || cfg.LockPath == cfg.StatePath {
		return fmt.Errorf("backup, lock, and state paths must be distinct and non-overlapping")
	}
	if err := validateDirectoryPath(cfg.BackupRoot, "Backup root"); err != nil {
		return err
	}
	return nil
}
