package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
)

const (
	word       = "C-Skills"
	pink       = "\x1b[1;38;5;201m"
	reset      = "\x1b[0m"
	frameDelay = 90 * time.Millisecond
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	_, noColor := os.LookupEnv("NO_COLOR")
	scriptDir, err := scriptDirectory()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	isTerminal := isatty.IsTerminal(os.Stdin.Fd()) && isatty.IsTerminal(os.Stdout.Fd())
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, isTerminal, noColor, environFromOS(), scriptDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// scriptDirectory mirrors install-skills.sh's SCRIPT_DIR: the resolved
// directory containing the running executable, used as the default base for
// managed backup/lock/state paths when their environment overrides are unset.
func scriptDirectory() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolve executable path: %w", err)
	}
	return filepath.Dir(resolved), nil
}

// environFromOS converts the process environment into the map[string]string
// shape ResolveConfiguration expects, allowing tests to inject an isolated
// environment instead.
func environFromOS() map[string]string {
	raw := os.Environ()
	environment := make(map[string]string, len(raw))
	for _, entry := range raw {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		environment[key] = value
	}
	return environment
}

func run(ctx context.Context, args []string, out, errOut io.Writer, isTerminal, noColor bool, environment map[string]string, scriptDir string) error {
	return runWithMenu(ctx, args, out, errOut, isTerminal, noColor, environment, scriptDir, runInteractiveMenu)
}

func runWithMenu(ctx context.Context, args []string, out, errOut io.Writer, isTerminal, noColor bool, environment map[string]string, scriptDir string, menuRunner func(context.Context, io.Writer, bool, bool, map[string]string, string) error) error {
	flags := flag.NewFlagSet("cskill", flag.ContinueOnError)
	flags.SetOutput(errOut)
	static := flags.Bool("static", false, "print the logo without animation or color")
	help := flags.Bool("help", false, "show command help")
	flags.BoolVar(help, "h", false, "show command help")
	flags.Usage = func() { printHelp(errOut) }
	if err := flags.Parse(args); err != nil {
		return err
	}

	if *help {
		printHelp(out)
		return nil
	}
	commands := flags.Args()
	if len(commands) == 1 && commands[0] == "menu" {
		if !isTerminal {
			return fmt.Errorf("menu requires an interactive terminal")
		}
		return menuRunner(ctx, out, noColor, *static, environment, scriptDir)
	}
	if len(commands) == 0 && isTerminal && !noColor && !*static {
		return menuRunner(ctx, out, false, false, environment, scriptDir)
	}
	if err := dispatchCommand(commands, out, errOut, environment, scriptDir); err != nil {
		return err
	}
	if len(flags.Args()) > 0 {
		return nil
	}
	animate := shouldAnimate(isTerminal, noColor, *static)
	if !animate {
		_, err := fmt.Fprintln(out, word)
		return err
	}

	return play(ctx, out, true)
}

// dispatchCommand routes an explicit command to its handler. With no command
// it returns immediately without touching configuration, so the animated or
// static logo path in run never performs installer work.
func dispatchCommand(args []string, out, errOut io.Writer, environment map[string]string, scriptDir string) error {
	if len(args) == 0 {
		return nil
	}
	command, rest := args[0], args[1:]

	switch command {
	case "help":
		if len(rest) != 0 {
			return fmt.Errorf("unexpected arguments for %q; run 'cskill help' for usage", command)
		}
		printHelp(out)
		return nil
	case "status":
		if len(rest) != 0 {
			return fmt.Errorf("unexpected arguments for %q; run 'cskill help' for usage", command)
		}
		return runStatusCommand(out, environment, scriptDir)
	case "list":
		if len(rest) != 0 {
			return fmt.Errorf("unexpected arguments for %q; run 'cskill help' for usage", command)
		}
		return runListCommand(out, errOut, environment, scriptDir)
	case "install":
		return runInstallCommand(rest, out, environment, scriptDir)
	case "restore":
		return runRestoreCommand(rest, out, environment, scriptDir)
	default:
		return fmt.Errorf("unknown command %q; run 'cskill help' for usage", command)
	}
}

func printHelp(out io.Writer) {
	fmt.Fprint(out, `Usage: cskill [--static] [command]

With no command, open the interactive menu on a terminal; otherwise print the
C-Skills logo. The menu requires a terminal. --static and NO_COLOR preserve the
plain no-argument logo behavior.

Commands:
  menu                Open the interactive installer menu (terminal required)
  help                Show this help
  status              Compare the installed skill-pack version against the
                       configured remote branch
  list                List managed backup snapshots
  install [--force]   Clone, validate, and install the configured skill pack
                       into the OpenCode, Claude, AGY, and Agents targets
  restore BACKUP_ID   Restore a managed snapshot by ID (required; there is no
                       interactive picker)

The menu never installs on entry. It shows all four resolved targets and
requires explicit confirmation before replacement.
install and restore always require an explicit command and argument; no command
ever performs a mutation implicitly, including opening/cancelling the menu or
with no arguments.
`)
}

func shouldAnimate(isTerminal, noColor, forceStatic bool) bool {
	return isTerminal && !noColor && !forceStatic
}

func frameText(frame int) string {
	if frame < 0 {
		frame = 0
	}
	if frame > len(word) {
		frame = len(word)
	}
	return word[:frame]
}

func renderFrame(frame int, colored bool) string {
	text := "* " + frameText(frame) + " *"
	if colored {
		return pink + text + reset
	}
	return text
}

func play(ctx context.Context, out io.Writer, colored bool) error {
	if colored {
		defer func() {
			_, _ = io.WriteString(out, reset+"\n")
		}()
	}

	for frame := 1; frame <= len(word); frame++ {
		if _, err := io.WriteString(out, "\r"+renderFrame(frame, colored)); err != nil {
			return fmt.Errorf("write animation: %w", err)
		}
		if frame == len(word) {
			break
		}

		timer := time.NewTimer(frameDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			if errors.Is(ctx.Err(), context.Canceled) {
				return nil
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
