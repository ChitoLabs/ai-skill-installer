package main

import (
	"bytes"
	"context"
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
		{name: "install is explicitly unavailable", args: []string{"install"}, wantError: `command "install" is not implemented yet`},
		{name: "status is explicitly unavailable", args: []string{"status"}, wantError: `command "status" is not implemented yet`},
		{name: "list is explicitly unavailable", args: []string{"list"}, wantError: `command "list" is not implemented yet`},
		{name: "restore is explicitly unavailable", args: []string{"restore"}, wantError: `command "restore" is not implemented yet`},
		{name: "unknown command", args: []string{"remove"}, wantError: `unknown command "remove"`},
		{name: "extra command arguments", args: []string{"status", "--verbose"}, wantError: `unexpected arguments for "status"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := dispatchCommand(test.args, &output)
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
			var output, errorOutput bytes.Buffer
			err := run(context.Background(), test.args, &output, &errorOutput, test.isTerminal, test.noColor)
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

func TestRunDoesNotAnimateInstallerCommands(t *testing.T) {
	for _, command := range []string{"install", "status", "list", "restore"} {
		t.Run(command, func(t *testing.T) {
			var output, errorOutput bytes.Buffer
			err := run(context.Background(), []string{command}, &output, &errorOutput, true, false)
			if err == nil || !strings.Contains(err.Error(), "not implemented yet") {
				t.Fatalf("run() error = %v, want clear unsupported-command error", err)
			}
			if output.Len() != 0 {
				t.Errorf("installer command output = %q, want no misleading output", output.String())
			}
		})
	}
}

func TestRunHelpFlagUsesCommandHelp(t *testing.T) {
	for _, flag := range []string{"-help", "--help", "-h"} {
		t.Run(flag, func(t *testing.T) {
			var output, errorOutput bytes.Buffer
			if err := run(context.Background(), []string{flag}, &output, &errorOutput, true, false); err != nil {
				t.Fatalf("run() error = %v", err)
			}
			if !strings.Contains(output.String(), "Installer commands are unavailable") {
				t.Errorf("help output = %q, want explicit implementation status", output.String())
			}
			if strings.Contains(output.String(), "\r") {
				t.Errorf("help unexpectedly animated: %q", output.String())
			}
		})
	}
}
