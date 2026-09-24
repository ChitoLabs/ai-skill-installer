package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// CreateSnapshot captures all three configured targets and atomically publishes
// a Bash-compatible v2 snapshot beneath cfg.BackupRoot. It returns the snapshot ID.
func CreateSnapshot(cfg Configuration, reason, sourceCommit string) (string, error) {
	if reason != "install" && reason != "pre-restore" {
		return "", fmt.Errorf("invalid snapshot reason")
	}
	if sourceCommit != "unknown" && !commitPattern.MatchString(sourceCommit) {
		return "", fmt.Errorf("invalid snapshot source commit")
	}
	if cfg.BackupRoot == "" || !filepath.IsAbs(cfg.BackupRoot) {
		return "", fmt.Errorf("backup root must be an absolute path")
	}
	targets := []struct {
		name string
		path string
		meta *SnapshotTarget
	}{
		{name: "opencode", path: cfg.OpenCodeTarget},
		{name: "claude", path: cfg.ClaudeTarget},
		{name: "agy", path: cfg.AgyTarget},
	}
	metadata := SnapshotMetadata{
		FormatVersion: 2,
		CreatedUTC:    time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		Reason:        reason,
		SourceCommit:  sourceCommit,
	}
	targets[0].meta = &metadata.OpenCode
	targets[1].meta = &metadata.Claude
	targets[2].meta = &metadata.AGY
	for _, target := range targets {
		if target.path == "" || !filepath.IsAbs(target.path) {
			return "", fmt.Errorf("%s target must be an absolute path", target.name)
		}
		*target.meta = SnapshotTarget{Captured: true}
		info, err := os.Lstat(target.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", fmt.Errorf("inspect %s target: %w", target.name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("%s target is not a safe directory: %s", target.name, target.path)
		}
		count, err := countSnapshotManifests(target.path)
		if err != nil {
			return "", fmt.Errorf("inspect %s target: %w", target.name, err)
		}
		target.meta.Existed = true
		target.meta.SkillCount = count
	}

	if err := os.MkdirAll(cfg.BackupRoot, 0o700); err != nil {
		return "", fmt.Errorf("create backup root: %w", err)
	}
	rootInfo, err := os.Lstat(cfg.BackupRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("backup root is missing or unsafe: %s", cfg.BackupRoot)
	}

	for attempt := 0; attempt < 32; attempt++ {
		id, err := generateSnapshotID(time.Now().UTC())
		if err != nil {
			return "", err
		}
		metadata.ID = id
		finalPath := filepath.Join(cfg.BackupRoot, id)
		if _, err := os.Lstat(finalPath); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect final snapshot path: %w", err)
		}
		tempPath, err := os.MkdirTemp(cfg.BackupRoot, ".snapshot."+id+".")
		if err != nil {
			return "", fmt.Errorf("create temporary snapshot: %w", err)
		}
		published := false
		defer func() {
			if !published {
				_ = os.RemoveAll(tempPath)
			}
		}()
		for _, target := range targets {
			if !target.meta.Existed {
				continue
			}
			if err := copyTree(filepath.Join(tempPath, target.name), target.path); err != nil {
				return "", fmt.Errorf("copy %s target: %w", target.name, err)
			}
		}
		metadataBytes, err := SerializeSnapshotMetadata(metadata)
		if err != nil {
			return "", fmt.Errorf("serialize snapshot metadata: %w", err)
		}
		if err := os.WriteFile(filepath.Join(tempPath, "metadata"), metadataBytes, 0o600); err != nil {
			return "", fmt.Errorf("write snapshot metadata: %w", err)
		}
		if err := validateSnapshotTrees(tempPath, metadata); err != nil {
			return "", fmt.Errorf("validate temporary snapshot: %w", err)
		}
		if err := os.Rename(tempPath, finalPath); err != nil {
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return "", fmt.Errorf("publish snapshot: %w", err)
		}
		published = true
		return id, nil
	}
	return "", fmt.Errorf("could not generate a unique snapshot ID")
}

func generateSnapshotID(now time.Time) (string, error) {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate snapshot ID: %w", err)
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix[:]), nil
}

// copyTree preserves directory structure, file bytes and modes, modification
// times, and symlink text. WalkDir does not follow symlinks; special files fail
// explicitly instead of being silently omitted or accidentally blocking reads.
func copyTree(destination, source string) error {
	if err := os.Mkdir(destination, 0o700); err != nil {
		return err
	}
	type directory struct {
		path string
		mode fs.FileMode
		mod  time.Time
	}
	var directories []directory
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if path == source {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("source root is not a safe directory")
			}
			directories = append(directories, directory{destination, info.Mode().Perm(), info.ModTime()})
			return nil
		}
		destinationPath := filepath.Join(destination, relative)
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, destinationPath)
		}
		if info.IsDir() {
			if err := os.Mkdir(destinationPath, 0o700); err != nil {
				return err
			}
			directories = append(directories, directory{destinationPath, info.Mode().Perm(), info.ModTime()})
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported special file in source tree: %s", path)
		}
		if err := copyRegularFile(destinationPath, path, info); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	for i := len(directories) - 1; i >= 0; i-- {
		dir := directories[i]
		if err := os.Chmod(dir.path, dir.mode); err != nil {
			return err
		}
		if err := os.Chtimes(dir.path, dir.mod, dir.mod); err != nil {
			return err
		}
	}
	return nil
}

func copyRegularFile(destination, source string, info fs.FileInfo) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		_ = in.Close()
		return err
	}
	_, copyErr := io.Copy(out, in)
	inErr := in.Close()
	if copyErr != nil {
		_ = out.Close()
		return copyErr
	}
	if inErr != nil {
		_ = out.Close()
		return inErr
	}
	if err := out.Chmod(info.Mode().Perm()); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chtimes(destination, info.ModTime(), info.ModTime())
}
