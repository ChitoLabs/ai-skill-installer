package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// CreateSnapshot captures all three configured targets and atomically publishes
// a Bash-compatible v2 snapshot beneath cfg.BackupRoot. It returns the snapshot ID.
func CreateSnapshot(cfg Configuration, reason, sourceCommit string) (string, error) {
	return createSnapshot(cfg, reason, sourceCommit, snapshotCreateHooks{})
}

type snapshotCreateHooks struct {
	newID         func(time.Time) (string, error)
	afterCopy     func() error
	beforePublish func(string) error
}

func createSnapshot(cfg Configuration, reason, sourceCommit string, hooks snapshotCreateHooks) (string, error) {
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
	sourceFingerprints := make(map[string][sha256.Size]byte)
	for _, target := range targets {
		if !target.meta.Existed {
			continue
		}
		fingerprint, err := fingerprintTree(target.path)
		if err != nil {
			return "", fmt.Errorf("fingerprint %s target: %w", target.name, err)
		}
		sourceFingerprints[target.name] = fingerprint
	}

	if err := os.MkdirAll(cfg.BackupRoot, 0o700); err != nil {
		return "", fmt.Errorf("create backup root: %w", err)
	}
	rootInfo, err := os.Lstat(cfg.BackupRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("backup root is missing or unsafe: %s", cfg.BackupRoot)
	}

	for attempt := 0; attempt < 32; attempt++ {
		newID := hooks.newID
		if newID == nil {
			newID = generateSnapshotID
		}
		id, err := newID(time.Now().UTC())
		if err != nil {
			return "", err
		}
		metadata.ID = id
		finalPath := filepath.Join(cfg.BackupRoot, id)
		attemptErr := createSnapshotAttempt(cfg.BackupRoot, finalPath, id, targets, metadata, sourceFingerprints, hooks)
		if errors.Is(attemptErr, errSnapshotPathExists) {
			continue
		}
		if attemptErr != nil {
			return "", attemptErr
		}
		return id, nil
	}
	return "", fmt.Errorf("could not generate a unique snapshot ID")
}

var errSnapshotPathExists = errors.New("snapshot path already exists")

func createSnapshotAttempt(backupRoot, finalPath, id string, targets []struct {
	name string
	path string
	meta *SnapshotTarget
}, metadata SnapshotMetadata, sourceFingerprints map[string][sha256.Size]byte, hooks snapshotCreateHooks) (result error) {
	tempPath, err := os.MkdirTemp(backupRoot, ".snapshot."+id+".")
	if err != nil {
		return fmt.Errorf("create temporary snapshot: %w", err)
	}
	published := false
	defer func() {
		if !published {
			if cleanupErr := os.RemoveAll(tempPath); cleanupErr != nil {
				result = errors.Join(result, fmt.Errorf("remove temporary snapshot: %w", cleanupErr))
			}
		}
	}()
	for _, target := range targets {
		if !target.meta.Existed {
			continue
		}
		if err := copyTree(filepath.Join(tempPath, target.name), target.path); err != nil {
			return fmt.Errorf("copy %s target: %w", target.name, err)
		}
	}
	if hooks.afterCopy != nil {
		if err := hooks.afterCopy(); err != nil {
			return fmt.Errorf("after-copy hook: %w", err)
		}
	}
	metadataBytes, err := SerializeSnapshotMetadata(metadata)
	if err != nil {
		return fmt.Errorf("serialize snapshot metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(tempPath, "metadata"), metadataBytes, 0o600); err != nil {
		return fmt.Errorf("write snapshot metadata: %w", err)
	}
	if err := validateSnapshotTrees(tempPath, metadata); err != nil {
		return fmt.Errorf("validate temporary snapshot: %w", err)
	}
	if hooks.beforePublish != nil {
		if err := hooks.beforePublish(finalPath); err != nil {
			return fmt.Errorf("before-publish hook: %w", err)
		}
	}
	for _, target := range targets {
		if !target.meta.Existed {
			continue
		}
		sourceAfter, err := fingerprintTree(target.path)
		if err != nil {
			return fmt.Errorf("recheck %s target before publish: %w", target.name, err)
		}
		copyFingerprint, err := fingerprintTree(filepath.Join(tempPath, target.name))
		if err != nil {
			return fmt.Errorf("verify copied %s target before publish: %w", target.name, err)
		}
		if sourceAfter != sourceFingerprints[target.name] || copyFingerprint != sourceFingerprints[target.name] {
			return fmt.Errorf("%s target changed during snapshot capture", target.name)
		}
	}
	if err := publishSnapshotNoReplace(tempPath, finalPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return errSnapshotPathExists
		}
		return fmt.Errorf("publish snapshot: %w", err)
	}
	published = true
	return nil
}

func fingerprintTree(root string) ([sha256.Size]byte, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		writeFingerprintString(h, filepath.ToSlash(relative))
		var mode [8]byte
		binary.LittleEndian.PutUint64(mode[:], uint64(info.Mode()))
		_, _ = h.Write(mode[:])
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			writeFingerprintString(h, link)
		case info.IsDir():
		case info.Mode().IsRegular():
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			contentHash := sha256.New()
			_, copyErr := io.Copy(contentHash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			_, _ = h.Write(contentHash.Sum(nil))
		default:
			return fmt.Errorf("unsupported special file in source tree: %s", path)
		}
		return nil
	})
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	var fingerprint [sha256.Size]byte
	copy(fingerprint[:], h.Sum(nil))
	return fingerprint, nil
}

func writeFingerprintString(h hash.Hash, value string) {
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = io.Copy(h, strings.NewReader(value))
}

func generateSnapshotID(now time.Time) (string, error) {
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate snapshot ID: %w", err)
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix[:]), nil
}

// copyTree preserves directory structure, file bytes and modes, modification
// times, symlink text, and hard-link relationships among files copied from
// the same source tree (matching cp -a's --preserve=links). WalkDir does not
// follow symlinks; special files fail explicitly instead of being silently
// omitted or accidentally blocking reads.
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
	hardlinkDestinations := make(map[[2]uint64]string)
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
		if key, hasMultipleLinks, ok := hardlinkIdentity(info); ok && hasMultipleLinks {
			if existing, seen := hardlinkDestinations[key]; seen {
				return os.Link(existing, destinationPath)
			}
			hardlinkDestinations[key] = destinationPath
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
