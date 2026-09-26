package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestFrameTextRevealsLogoAndClamps(t *testing.T) {
	for frame := 0; frame <= len(word); frame++ {
		got := frameText(frame)
		if got != word[:frame] {
			t.Errorf("frameText(%d) = %q, want %q", frame, got, word[:frame])
		}
	}
	if got := frameText(len(word) + 1); got != word {
		t.Errorf("frameText past end = %q, want %q", got, word)
	}
	if got := frameText(-1); got != "" {
		t.Errorf("negative frame = %q, want empty string", got)
	}
}

func TestRenderFrameHasPlainAndResettablePinkVariants(t *testing.T) {
	plain := renderFrame(len(word), false)
	if plain != "* C-Skills *" || strings.Contains(plain, "\x1b") {
		t.Errorf("plain render = %q, want printable unstyled logo", plain)
	}

	colored := renderFrame(len(word), true)
	if !strings.HasPrefix(colored, pink) || !strings.HasSuffix(colored, reset) {
		t.Errorf("colored render lacks pink/reset sequence: %q", colored)
	}
}

func TestShouldAnimateRequiresTTYAndOptInConditions(t *testing.T) {
	tests := []struct {
		name                             string
		isTerminal, noColor, forceStatic bool
		want                             bool
	}{
		{name: "interactive terminal", isTerminal: true, want: true},
		{name: "non-terminal", noColor: false, want: false},
		{name: "NO_COLOR", isTerminal: true, noColor: true, want: false},
		{name: "static option", isTerminal: true, forceStatic: true, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := shouldAnimate(test.isTerminal, test.noColor, test.forceStatic); got != test.want {
				t.Errorf("shouldAnimate() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestDispatchCommand(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantOutput string
		wantError  string
	}{
		{name: "help command", args: []string{"help"}, wantOutput: "Usage: cskill"},
		{name: "help rejects arguments", args: []string{"help", "extra"}, wantError: `unexpected arguments for "help"`},
		{name: "unknown command", args: []string{"remove"}, wantError: `unknown command "remove"`},
		{name: "status rejects extra arguments", args: []string{"status", "--verbose"}, wantError: `unexpected arguments for "status"`},
		{name: "list rejects extra arguments", args: []string{"list", "--verbose"}, wantError: `unexpected arguments for "list"`},
		{name: "install rejects unknown flag", args: []string{"install", "--verbose"}, wantError: "install accepts only an optional --force argument"},
		{name: "install rejects extra arguments", args: []string{"install", "--force", "extra"}, wantError: "install accepts only an optional --force argument"},
		{name: "restore rejects extra arguments", args: []string{"restore", "one", "two"}, wantError: "restore accepts at most one BACKUP_ID"},
		{name: "restore without ID reports the gap before touching configuration", args: []string{"restore"}, wantError: "restore requires a BACKUP_ID"},
		{name: "restore rejects an unsafe backup ID", args: []string{"restore", "../evil"}, wantError: "unsafe backup ID"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, root := restoreFixture(t)
			var output, errOutput bytes.Buffer
			err := dispatchCommand(test.args, &output, &errOutput, commandEnvironment(cfg), root)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("error = %v, want text %q", err, test.wantError)
				}
				if output.Len() != 0 {
					t.Fatalf("unsupported command produced output: %q", output.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("dispatchCommand() error = %v", err)
			}
			if !strings.Contains(output.String(), test.wantOutput) {
				t.Errorf("output = %q, want it to contain %q", output.String(), test.wantOutput)
			}
		})
	}
}

// commandEnvironment rebuilds the environment map a fixture's Configuration
// was resolved from, so dispatchCommand can be exercised end-to-end without
// touching the real process environment.
func commandEnvironment(cfg Configuration) map[string]string {
	// A dedicated sibling directory keeps clone staging isolated from HOME's
	// protected subtree (e.g. .config/opencode/commands), which the parent
	// fixture root also contains.
	tmpRoot := filepath.Join(filepath.Dir(cfg.BackupRoot), "tmp")
	_ = os.MkdirAll(tmpRoot, 0o700)
	return map[string]string{
		"HOME":                       cfg.HomeDir,
		"TMPDIR":                     tmpRoot,
		"OPENCODE_SKILLS_DIR":        cfg.OpenCodeTarget,
		"CLAUDE_SKILLS_DIR":          cfg.ClaudeTarget,
		"AGY_SKILLS_DIR":             cfg.AgyTarget,
		"SKILL_BACKUP_DIR":           cfg.BackupRoot,
		"SKILL_INSTALLER_LOCK_FILE":  cfg.LockPath,
		"SKILL_INSTALLER_STATE_FILE": cfg.StatePath,
		"SKILL_PACK_REPOSITORY_URL":  cfg.RepositoryURL,
		"SKILL_PACK_BRANCH":          cfg.Branch,
		"SKILL_PACK_MIN_SKILL_COUNT": strconv.Itoa(cfg.MinimumSkillCount),
	}
}

func TestDispatchCommandWithNoArgumentsTouchesNothing(t *testing.T) {
	cfg, root := restoreFixture(t)
	var output, errOutput bytes.Buffer
	if err := dispatchCommand(nil, &output, &errOutput, commandEnvironment(cfg), root); err != nil {
		t.Fatalf("dispatchCommand() error = %v", err)
	}
	if output.Len() != 0 || errOutput.Len() != 0 {
		t.Fatalf("no-command dispatch produced output: out=%q err=%q", output.String(), errOutput.String())
	}
	for _, path := range []string{cfg.BackupRoot, cfg.LockPath, cfg.StatePath} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Errorf("no-command dispatch touched managed path %s (err=%v)", path, err)
		}
	}
}

func TestRunKeepsLogoStaticOutsideInteractiveAnimation(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		isTerminal bool
		noColor    bool
	}{
		{name: "static flag on terminal", args: []string{"-static"}, isTerminal: true},
		{name: "non-terminal", isTerminal: false},
		{name: "NO_COLOR terminal", isTerminal: true, noColor: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, root := restoreFixture(t)
			var output, errorOutput bytes.Buffer
			err := run(context.Background(), test.args, &output, &errorOutput, test.isTerminal, test.noColor, commandEnvironment(cfg), root)
			if err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if got := output.String(); got != word+"\n" {
				t.Errorf("output = %q, want static logo", got)
			}
			if strings.Contains(output.String(), "\x1b") || strings.Contains(output.String(), "\r") {
				t.Errorf("static output contains animation or ANSI control characters: %q", output.String())
			}
		})
	}
}

// TestRunDoesNotAnimateInstallerCommands proves that dispatching a real
// installer command never emits animation control bytes, whether the command
// succeeds (list on an empty backup root) or fails fast on invalid usage
// (restore/install without arguments touch no managed path either way).
func TestRunDoesNotAnimateInstallerCommands(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantError string
	}{
		{name: "list", args: []string{"list"}},
		{name: "restore without ID", args: []string{"restore"}, wantError: "restore requires a BACKUP_ID"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg, root := restoreFixture(t)
			var output, errorOutput bytes.Buffer
			err := run(context.Background(), test.args, &output, &errorOutput, true, false, commandEnvironment(cfg), root)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("run() error = %v, want text %q", err, test.wantError)
				}
			} else if err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if strings.Contains(output.String(), "\x1b") || strings.Contains(output.String(), "\r") {
				t.Errorf("installer command output animated: %q", output.String())
			}
		})
	}
}

func TestPrintHelpDescribesEveryCommand(t *testing.T) {
	var out bytes.Buffer
	printHelp(&out)
	for _, want := range []string{"status", "list", "install [--force]", "restore BACKUP_ID", "help"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help text missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunHelpFlagUsesCommandHelp(t *testing.T) {
	for _, flag := range []string{"-help", "--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			cfg, root := restoreFixture(t)
			var output, errorOutput bytes.Buffer
			if err := run(context.Background(), []string{flag}, &output, &errorOutput, true, false, commandEnvironment(cfg), root); err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if !strings.Contains(output.String(), "install and restore always require an explicit command and argument") {
				t.Errorf("help output = %q, want explicit no-implicit-mutation guarantee", output.String())
			}
			if strings.Contains(output.String(), "\r") {
				t.Errorf("help unexpectedly animated: %q", output.String())
			}
		})
	}
}
