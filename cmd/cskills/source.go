package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// ValidateSkillTree rejects unsafe source trees and requires at least minimum
// direct-child SKILL.md manifests. expectedCount, when positive, adds an exact
// count assertion for callers validating an already-recorded tree.
func ValidateSkillTree(root string, minimum, expectedCount int) (int, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("skill directory not found or unsafe: %s", root)
	}
	if filepath.Base(filepath.Clean(root)) == ".codegraph" {
		return 0, fmt.Errorf("skill directory cannot be named .codegraph: %s", root)
	}
	if minimum <= 0 {
		return 0, fmt.Errorf("minimum skill count must be a positive integer")
	}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("incoming skill tree contains a symbolic link: %s", path)
		}
		if path != root && entry.IsDir() && entry.Name() == ".codegraph" {
			return fmt.Errorf("skill tree contains a protected .codegraph directory: %s", path)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, fmt.Errorf("read skill directory %s: %w", root, err)
	}
	count := 0
	for _, entry := range entries {
		manifest := filepath.Join(root, entry.Name(), "SKILL.md")
		manifestInfo, err := os.Lstat(manifest)
		if os.IsNotExist(err) || isNotDirectory(err) {
			continue
		}
		if err != nil {
			return 0, fmt.Errorf("inspect skill manifest %s: %w", manifest, err)
		}
		if !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
			return 0, fmt.Errorf("skill manifest is not a regular non-symlink file: %s", manifest)
		}
		count++
	}
	if count < minimum {
		return count, fmt.Errorf("skill validation found %d manifests; minimum is %d in %s", count, minimum, root)
	}
	if expectedCount > 0 && count != expectedCount {
		return count, fmt.Errorf("skill validation expected %d manifests but found %d in %s", expectedCount, count, root)
	}
	return count, nil
}

func isNotDirectory(err error) bool {
	return os.IsNotExist(err) || errors.Is(err, syscall.ENOTDIR)
}
