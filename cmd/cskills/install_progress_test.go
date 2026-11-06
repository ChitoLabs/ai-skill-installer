package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCancellationBeforePreparationDoesNotMutate(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL = "file://" + repository
	cfg.Branch = "main"
	cfg.MinimumSkillCount = 1
	environment := commandEnvironment(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var events []installEvent
	var out bytes.Buffer

	err := runInstallWithProgress(ctx, cfg, false, environment, &out, func(event installEvent) {
		events = append(events, event)
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("runInstallWithProgress() error = %v, want context.Canceled", err)
	}
	if len(events) != 0 {
		t.Errorf("canceled install emitted progress events: %#v", events)
	}
	for _, path := range []string{cfg.BackupRoot, cfg.StatePath, cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget, cfg.AgentsTarget} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("canceled install mutated %s: Lstat error = %v", path, err)
		}
	}
	entries, err := os.ReadDir(environment["TMPDIR"])
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "cskill-installer.") {
			t.Errorf("canceled install left clone staging entry %q", entry.Name())
		}
	}
}

func TestInstallProgressReportsPreparationAndAtomicPhases(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL = "file://" + repository
	cfg.Branch = "main"
	cfg.MinimumSkillCount = 1
	var events []installEvent
	var out bytes.Buffer

	if err := runInstallWithProgress(context.Background(), cfg, false, commandEnvironment(cfg), &out, func(event installEvent) {
		events = append(events, event)
	}); err != nil {
		t.Fatalf("runInstallWithProgress() error = %v\n%s", err, out.String())
	}
	if len(events) == 0 {
		t.Fatal("install emitted no progress events")
	}
	atomicSeen := false
	for i, event := range events {
		if event.Phase == "atomic" && !event.Atomic {
			t.Errorf("atomic boundary event did not set Atomic: %#v", event)
		}
		if event.Atomic {
			atomicSeen = true
		}
		if atomicSeen && !event.Atomic {
			t.Errorf("event %d cleared Atomic after transaction began: %#v", i, event)
		}
	}
	if !atomicSeen {
		t.Fatal("install never reported entry into atomic transaction")
	}
	for _, phase := range []string{"lock", "clone", "validate", "comparison", "snapshot", "staging", "apply", "verify", "cleanup", "state"} {
		if !hasInstallPhase(events, phase) {
			t.Errorf("progress events missing phase %q: %#v", phase, events)
		}
	}
}

func TestInstallCancellationAfterAtomicDoesNotInterruptCommit(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL = "file://" + repository
	cfg.Branch = "main"
	cfg.MinimumSkillCount = 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var events []installEvent

	err := runInstallWithProgress(ctx, cfg, false, commandEnvironment(cfg), &bytes.Buffer{}, func(event installEvent) {
		events = append(events, event)
		if event.Phase == "atomic" {
			cancel()
		}
	})
	if err != nil {
		t.Fatalf("install interrupted by cancellation after atomic boundary: %v", err)
	}
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget, cfg.AgentsTarget} {
		if _, err := os.Stat(target); err != nil {
			t.Errorf("atomic install did not replace %s: %v", target, err)
		}
	}
	if _, err := os.Stat(cfg.StatePath); err != nil {
		t.Errorf("atomic install did not persist state: %v", err)
	}
	atomicSeen := false
	for _, event := range events {
		if event.Phase == "atomic" {
			atomicSeen = true
		}
		if atomicSeen && !event.Atomic {
			t.Errorf("post-boundary phase did not retain Atomic=true: %#v", event)
		}
	}
}

func TestInstallProgressSignalsSnapshotBeforeWritingRecoveryData(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL, cfg.Branch, cfg.MinimumSkillCount = "file://"+repository, "main", 1
	seen := false
	err := runInstallWithProgress(context.Background(), cfg, false, commandEnvironment(cfg), &bytes.Buffer{}, func(event installEvent) {
		if event.Phase == "snapshot" {
			seen = true
			if _, err := os.Lstat(cfg.BackupRoot); !os.IsNotExist(err) {
				t.Errorf("snapshot phase must be emitted before recovery data is created; Lstat = %v", err)
			}
		}
	})
	if err != nil || !seen {
		t.Fatalf("install error = %v, snapshot event seen = %t", err, seen)
	}
}

func TestInstallCancellationDuringPreparationLeavesTargetsAndStateAbsent(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL, cfg.Branch, cfg.MinimumSkillCount = "file://"+repository, "main", 1
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := runInstallWithProgress(ctx, cfg, false, commandEnvironment(cfg), &bytes.Buffer{}, func(event installEvent) {
		if event.Phase == "comparison" {
			cancel()
		}
		if event.Atomic {
			t.Error("cancelled preparation entered atomic transaction")
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	for _, path := range []string{cfg.BackupRoot, cfg.StatePath, cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget, cfg.AgentsTarget} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("cancelled preparation mutated %s: %v", path, err)
		}
	}
}

func TestInstallProgressCreatesMissingTargetParents(t *testing.T) {
	cfg, root := restoreFixture(t)
	repository := createLocalSkillRepository(t, root)
	cfg.RepositoryURL, cfg.Branch, cfg.MinimumSkillCount = "file://"+repository, "main", 1
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget, cfg.AgentsTarget} {
		if err := os.Remove(filepath.Dir(target)); err != nil {
			t.Fatal(err)
		}
	}
	if err := runInstallWithProgress(context.Background(), cfg, false, commandEnvironment(cfg), &bytes.Buffer{}, nil); err != nil {
		t.Fatalf("install into missing target parents: %v", err)
	}
	for _, target := range []string{cfg.OpenCodeTarget, cfg.ClaudeTarget, cfg.AgyTarget, cfg.AgentsTarget} {
		if info, err := os.Stat(target); err != nil || !info.IsDir() {
			t.Errorf("target not installed: %s, err=%v", target, err)
		}
	}
}

func hasInstallPhase(events []installEvent, phase string) bool {
	for _, event := range events {
		if event.Phase == phase {
			return true
		}
	}
	return false
}
