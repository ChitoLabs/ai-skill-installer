package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"
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

	info, err := os.Stdout.Stat()
	if err != nil {
		fmt.Fprintln(os.Stderr, fmt.Errorf("check terminal: %w", err))
		os.Exit(1)
	}
	_, noColor := os.LookupEnv("NO_COLOR")
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr, info.Mode()&os.ModeCharDevice != 0, noColor); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer, isTerminal, noColor bool) error {
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
	if err := dispatchCommand(flags.Args(), out); err != nil {
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

func dispatchCommand(args []string, out io.Writer) error {
	if len(args) == 0 {
		return nil
	}
	if len(args) > 1 {
		return fmt.Errorf("unexpected arguments for %q; run 'cskill help' for usage", args[0])
	}

	switch args[0] {
	case "help":
		printHelp(out)
		return nil
	case "install", "status", "list", "restore":
		return fmt.Errorf("command %q is not implemented yet", args[0])
	default:
		return fmt.Errorf("unknown command %q; run 'cskill help' for usage", args[0])
	}
}

func printHelp(out io.Writer) {
	fmt.Fprint(out, `Usage: cskill [--static] [command]

With no command, print the C-Skills logo. Animation is shown only on a terminal
when NO_COLOR is unset and --static is not specified.

Commands:
  help      Show this help
  install   Not implemented yet
  status    Not implemented yet
  list      Not implemented yet
  restore   Not implemented yet

Installer commands are unavailable in this version; no files are changed.
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
