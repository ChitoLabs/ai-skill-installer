package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
)

// referenceTarget mirrors reference_target: it returns the first configured
// skill target (OpenCode, then Claude, then AGY) that exists as a real,
// non-symlink directory, or "" when none of the three targets exist yet.
func referenceTarget(cfg Configuration) string {
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget} {
		if info, err := os.Lstat(target); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return target
		}
	}
	return ""
}

// targetsMatchSource mirrors targets_match_source: it reports whether the
// given reference target is byte-identical to the cloned skill source,
// using the same `diff -qr --no-dereference` comparison Bash relies on. A
// missing/unsafe reference or any diff failure (a real difference or a
// comparison error) is treated as "does not match", exactly as Bash's
// `if targets_match_source ...` treats any non-zero exit.
func targetsMatchSource(sourceDir, reference string) bool {
	if reference == "" {
		return false
	}
	if info, err := os.Stat(reference); err != nil || !info.IsDir() {
		return false
	}
	return exec.Command("diff", "-qr", "--no-dereference", "--", sourceDir, reference).Run() == nil
}

// treesDiffer mirrors the per-skill comparison inside summarize_skill_changes:
// `diff -qr --no-dereference` between two same-named skill directories. Any
// non-zero exit (a real difference or a comparison error) counts as modified.
func treesDiffer(oldPath, newPath string) bool {
	return exec.Command("diff", "-qr", "--no-dereference", "--", oldPath, newPath).Run() != nil
}

// listSkillNames mirrors list_skill_names: the sorted, unique set of direct
// child directory names under root that have a regular, non-symlink
// SKILL.md manifest. A missing or non-directory root yields an empty list,
// not an error, matching Bash's `[[ -d "$root" ]] || return 0`.
func listSkillNames(root string) ([]string, error) {
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() {
		return nil, nil
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read skill directory %s: %w", root, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		manifest := filepath.Join(root, entry.Name(), "SKILL.md")
		manifestInfo, statErr := os.Lstat(manifest)
		if statErr != nil {
			continue
		}
		if manifestInfo.Mode().IsRegular() && manifestInfo.Mode()&os.ModeSymlink == 0 {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// skillChangeSummary mirrors the added/modified/removed sets summarize_skill_changes
// computes between an old and a new skill tree.
type skillChangeSummary struct {
	added, modified, removed []string
}

// summarizeSkillChanges mirrors summarize_skill_changes: it lists skills
// present only in newRoot as added, present only in oldRoot as removed, and
// present in both but with differing content (per treesDiffer) as modified.
func summarizeSkillChanges(oldRoot, newRoot string) (skillChangeSummary, error) {
	oldNames, err := listSkillNames(oldRoot)
	if err != nil {
		return skillChangeSummary{}, err
	}
	newNames, err := listSkillNames(newRoot)
	if err != nil {
		return skillChangeSummary{}, err
	}
	oldSet := make(map[string]bool, len(oldNames))
	for _, name := range oldNames {
		oldSet[name] = true
	}
	newSet := make(map[string]bool, len(newNames))
	for _, name := range newNames {
		newSet[name] = true
	}

	var summary skillChangeSummary
	for _, name := range newNames {
		if !oldSet[name] {
			summary.added = append(summary.added, name)
		} else if treesDiffer(filepath.Join(oldRoot, name), filepath.Join(newRoot, name)) {
			summary.modified = append(summary.modified, name)
		}
	}
	for _, name := range oldNames {
		if !newSet[name] {
			summary.removed = append(summary.removed, name)
		}
	}
	sort.Strings(summary.added)
	sort.Strings(summary.modified)
	sort.Strings(summary.removed)
	return summary, nil
}

// writeSkillChangeSummary mirrors summarize_skill_changes's log output: a
// one-line +added/~modified/-removed count, followed by up to 20 names per
// non-empty category, or a single "no differences" line when all are empty.
func writeSkillChangeSummary(out io.Writer, summary skillChangeSummary) {
	fmt.Fprintf(out, "Skills: +%d new / ~%d modified / -%d removed\n",
		len(summary.added), len(summary.modified), len(summary.removed))

	printNames := func(label, prefix string, names []string) {
		if len(names) == 0 {
			return
		}
		fmt.Fprintln(out, label)
		if len(names) > 20 {
			names = names[:20]
		}
		for _, name := range names {
			fmt.Fprintf(out, "  %s %s\n", prefix, name)
		}
	}
	printNames("New skills:", "+", summary.added)
	printNames("Modified skills:", "~", summary.modified)
	printNames("Removed skills:", "-", summary.removed)

	if len(summary.added) == 0 && len(summary.modified) == 0 && len(summary.removed) == 0 {
		fmt.Fprintln(out, "No skill content differences detected.")
	}
}
