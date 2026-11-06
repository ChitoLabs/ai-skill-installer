package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestTUIInstallRequiresExplicitConfirmation(t *testing.T) {
	dispatched := 0
	model := newTUIModel(Configuration{
		OpenCodeTarget: "/home/test/.config/opencode/skills",
		ClaudeTarget:   "/home/test/.claude/skills",
		AgyTarget:      "/home/test/.gemini/config/skills",
		AgentsTarget:   "/home/test/.agents/skills",
	}, false, true, nil, "/tmp", tuiDependencies{
		install: func(context.Context, Configuration, bool, map[string]string, io.Writer, func(installEvent)) error {
			dispatched++
			return nil
		},
	})

	confirm, _ := updateTUI(model, tea.KeyMsg{Type: tea.KeyEnter})
	if confirm.screen != screenConfirm || confirm.confirm != confirmCancel {
		t.Fatalf("initial install confirmation = screen %v, selection %v; want cancel selected", confirm.screen, confirm.selected)
	}
	if dispatched != 0 {
		t.Fatalf("opening confirmation dispatched install %d times", dispatched)
	}
	view := confirm.View()
	for _, target := range []string{"/home/test/.config/opencode/skills", "/home/test/.claude/skills", "/home/test/.gemini/config/skills", "/home/test/.agents/skills", "replace"} {
		if !contains(view, target) {
			t.Errorf("confirmation view missing %q: %s", target, view)
		}
	}
	if !contains(view, "> Cancel") || !contains(view, "  Replace all targets") || strings.Index(view, "Cancel") > strings.Index(view, "Replace all targets") {
		t.Fatalf("confirmation choices must show Cancel first and selected by default: %q", view)
	}
	got, _ := updateTUI(confirm, tea.KeyMsg{Type: tea.KeyEnter})
	if got.screen != screenMenu || dispatched != 0 {
		t.Fatalf("cancel confirmation = screen %v, installs %d; want menu and none", got.screen, dispatched)
	}
}

func TestTUIConfirmInstallDispatchesOnlyAfterSelection(t *testing.T) {
	dispatched := 0
	model := testTUIModel(tuiDependencies{
		install: func(context.Context, Configuration, bool, map[string]string, io.Writer, func(installEvent)) error {
			dispatched++
			return nil
		},
	})
	model.screen = screenConfirm
	model.confirm = confirmCancel
	model, _ = updateTUI(model, tea.KeyMsg{Type: tea.KeyDown})
	if model.confirm != confirmProceed || dispatched != 0 {
		t.Fatalf("selecting proceed dispatched before confirmation: model=%+v installs=%d", model, dispatched)
	}
	model, cmd := updateTUI(model, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || model.screen != screenRunning || dispatched != 0 {
		t.Fatalf("confirm should schedule, not synchronously run, install: cmd=%v model=%+v installs=%d", cmd, model, dispatched)
	}
}

func TestTUINavigationKeysAndConfirmationOrder(t *testing.T) {
	model := testTUIModel(tuiDependencies{})
	model, _ = updateTUI(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if model.selected != 1 {
		t.Fatalf("j navigation selected %d, want next menu item", model.selected)
	}
	model, _ = updateTUI(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if model.selected != 0 {
		t.Fatalf("k navigation selected %d, want previous menu item", model.selected)
	}
	model.screen, model.confirm = screenConfirm, confirmCancel
	model, _ = updateTUI(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if model.confirm != confirmProceed {
		t.Fatal("j/down should select Replace after Cancel")
	}
	model, _ = updateTUI(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if model.confirm != confirmCancel {
		t.Fatal("k/up should select Cancel before Replace")
	}
}

func TestTUISpinnerTicksWithoutFakeProgress(t *testing.T) {
	model := testTUIModel(tuiDependencies{})
	model.screen, model.phase = screenRunning, "clone"
	first := model.View()
	if !contains(first, "[|") {
		t.Fatalf("running view missing initial ASCII spinner: %q", first)
	}
	model, cmd := updateTUI(model, spinnerTickMsg{})
	if cmd == nil || !contains(model.View(), "[/") || contains(model.View(), "%") {
		t.Fatalf("spinner tick view/cmd = %q / %v", model.View(), cmd)
	}
	for _, disabled := range []struct{ noColor, static bool }{{noColor: true}, {static: true}} {
		model.noColor, model.static = disabled.noColor, disabled.static
		if contains(model.View(), "[|") || contains(model.View(), "[/") {
			t.Fatalf("spinner remained enabled with noColor=%t static=%t: %q", model.noColor, model.static, model.View())
		}
	}
}

func TestTUIExternalCancelExitsMenuButWaitsForRunningBackend(t *testing.T) {
	model := testTUIModel(tuiDependencies{})
	model, cmd := updateTUI(model, externalCancelMsg{})
	if cmd == nil {
		t.Fatal("external cancellation on idle menu should quit")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("external cancellation command did not quit")
	}
	model = testTUIModel(tuiDependencies{})
	model.screen = screenRunning
	model, cmd = updateTUI(model, externalCancelMsg{})
	if cmd != nil || model.screen != screenRunning || !model.cancelRequested {
		t.Fatalf("external cancellation must wait for running backend: model=%+v cmd=%v", model, cmd)
	}
}

func TestTUICanceledBeforeConfirmationNeverStartsInstall(t *testing.T) {
	calls := 0
	model := testTUIModel(tuiDependencies{install: func(context.Context, Configuration, bool, map[string]string, io.Writer, func(installEvent)) error {
		calls++
		return nil
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	model.ctx = ctx
	model.screen, model.confirm = screenConfirm, confirmProceed
	model, cmd := updateTUI(model, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || calls != 0 || model.screen != screenResult {
		t.Fatalf("canceled model started install: calls=%d screen=%v cmd=%v", calls, model.screen, cmd)
	}
}

func TestTUIRuntimeExitCancelsAndWaitsForStartedBackend(t *testing.T) {
	started := make(chan struct{})
	completed := make(chan tea.Msg, 1)
	model := testTUIModel(tuiDependencies{
		install: func(ctx context.Context, _ Configuration, _ bool, _ map[string]string, _ io.Writer, _ func(installEvent)) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	})
	model.screen, model.confirm = screenConfirm, confirmProceed
	model, cmd := updateTUI(model, tea.KeyMsg{Type: tea.KeyEnter})
	batch := cmd().(tea.BatchMsg)
	backend := batch[0]
	wantRuntimeErr := errors.New("runtime failed")
	err := runTUIRuntime(model, func() (tea.Model, error) {
		go func() { completed <- backend() }()
		<-started
		return nil, wantRuntimeErr
	})
	if !errors.Is(err, wantRuntimeErr) {
		t.Fatalf("runtime error = %v, want %v", err, wantRuntimeErr)
	}
	select {
	case msg := <-completed:
		if !errors.Is(msg.(installFinishedMsg).err, context.Canceled) {
			t.Fatalf("backend result = %#v, want cancellation", msg)
		}
	default:
		t.Fatal("runtime returned before started backend completed")
	}
}

func TestTUIRuntimeExitBeforeCommandSchedulingDoesNotStartOrWait(t *testing.T) {
	dispatched := 0
	model := testTUIModel(tuiDependencies{install: func(context.Context, Configuration, bool, map[string]string, io.Writer, func(installEvent)) error {
		dispatched++
		return nil
	}})
	model.screen, model.confirm = screenConfirm, confirmProceed
	model, cmd := updateTUI(model, tea.KeyMsg{Type: tea.KeyEnter})
	_ = cmd // Runtime exit before Bubble Tea schedules this command.
	if err := runTUIRuntime(model, func() (tea.Model, error) { return nil, errors.New("EOF") }); err == nil {
		t.Fatal("runtime EOF error was lost")
	}
	if dispatched != 0 {
		t.Fatalf("unscheduled backend ran %d times", dispatched)
	}
}

func TestTUIRunningCancellationWaitsForBackend(t *testing.T) {
	model := testTUIModel(tuiDependencies{})
	model.screen = screenRunning
	canceled := false
	model.operation = newTUIOperation(func() { canceled = true })
	got, cmd := updateTUI(model, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || !got.cancelRequested || got.screen != screenRunning || !canceled {
		t.Fatalf("running cancel must request cancellation and keep UI alive: model=%+v cmd=%v", got, cmd)
	}
	model, _ = updateTUI(got, installFinishedMsg{err: context.Canceled})
	if model.screen != screenResult || !contains(model.View(), "Error: context canceled") {
		t.Fatalf("finished cancellation should show its result: screen=%v view=%q", model.screen, model.View())
	}
}

func TestTUIResultSurfacesPrivateOutputWarningsAndErrors(t *testing.T) {
	model := testTUIModel(tuiDependencies{
		status: func(out io.Writer, _ map[string]string, _ string) error {
			_, _ = io.WriteString(out, "Installed version: abc123\\n")
			return errors.New("remote unavailable")
		},
	})
	model.selected = 1
	model, cmd := updateTUI(model, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil || model.screen != screenRunning {
		t.Fatal("status action did not start asynchronously")
	}
	model, quitCmd := updateTUI(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if quitCmd != nil || model.screen != screenRunning || !model.cancelRequested {
		t.Fatal("q should wait for the active status operation")
	}
	message := runBackendCommand(t, cmd)
	finished := message.(taskFinishedMsg)
	if finished.output != "Installed version: abc123\\n" || finished.err == nil {
		t.Fatalf("private result = %+v", finished)
	}
	model, quitCmd = updateTUI(model, finished)
	if quitCmd != nil || model.screen != screenResult {
		t.Fatal("q cancellation should leave the completed status result visible")
	}
	view := model.View()
	if !contains(view, "Installed version: abc123") || !contains(view, "Error: remote unavailable") {
		t.Fatalf("result view omitted output or error: %q", view)
	}

	model = testTUIModel(tuiDependencies{
		list: func(out, warnings io.Writer, _ map[string]string, _ string) error {
			_, _ = io.WriteString(out, "backup-1\\n")
			_, _ = io.WriteString(warnings, "Skipping invalid snapshot\\n")
			return nil
		},
	})
	model.selected = 2
	model, cmd = updateTUI(model, tea.KeyMsg{Type: tea.KeyEnter})
	finished = runBackendCommand(t, cmd).(taskFinishedMsg)
	model, _ = updateTUI(model, finished)
	if !contains(model.View(), "Skipping invalid snapshot") {
		t.Fatalf("result view omitted private warning: %q", model.View())
	}
}

func TestTUIInstallProgressAndPrivateResult(t *testing.T) {
	var progress []tea.Msg
	dispatched := 0
	model := testTUIModel(tuiDependencies{
		install: func(_ context.Context, _ Configuration, _ bool, _ map[string]string, out io.Writer, emit func(installEvent)) error {
			dispatched++
			_, _ = io.WriteString(out, "Installed four targets\\n")
			event := installEvent{Phase: "backup", Atomic: true}
			emit(event)
			return errors.New("transaction failed")
		},
		emit: func(msg tea.Msg) { progress = append(progress, msg) },
	})
	model.screen, model.confirm = screenConfirm, confirmProceed
	model, cmd := updateTUI(model, tea.KeyMsg{Type: tea.KeyEnter})
	if dispatched != 0 || cmd == nil || model.screen != screenRunning {
		t.Fatalf("confirm dispatch state: calls=%d model=%+v cmd=%v", dispatched, model, cmd)
	}
	finished := runBackendCommand(t, cmd).(installFinishedMsg)
	if dispatched != 1 || finished.output != "Installed four targets\\n" || finished.err == nil {
		t.Fatalf("install result = %+v; dispatches=%d", finished, dispatched)
	}
	if len(progress) != 1 || progress[0].(installProgressMsg).event != (installEvent{Phase: "backup", Atomic: true}) {
		t.Fatalf("progress messages = %#v", progress)
	}
	model, _ = updateTUI(model, progress[0])
	if model.phase != "backup" || !model.atomic {
		t.Fatalf("progress state = phase %q atomic %t", model.phase, model.atomic)
	}
	model, _ = updateTUI(model, finished)
	if !contains(model.View(), "Installed four targets") || !contains(model.View(), "Error: transaction failed") {
		t.Fatalf("result view omitted install output/error: %q", model.View())
	}
}

func TestTUIAtomicProgressDefersCancellationUntilResult(t *testing.T) {
	model := testTUIModel(tuiDependencies{})
	model.screen, model.atomic = screenRunning, true
	model.operation = newTUIOperation(func() { t.Error("atomic cancellation must not interrupt backend") })
	model, cmd := updateTUI(model, tea.KeyMsg{Type: tea.KeyCtrlC})
	if cmd != nil || model.screen != screenRunning || !model.cancelRequested {
		t.Fatalf("atomic cancellation state = %+v cmd=%v", model, cmd)
	}
	model, cmd = updateTUI(model, installFinishedMsg{output: "installed\\n"})
	if cmd != nil || model.screen != screenResult || !contains(model.View(), "installed") {
		t.Fatalf("deferred cancellation did not leave result visible: model=%+v cmd=%v", model, cmd)
	}
}

func TestTUIProgressAndStaticRendering(t *testing.T) {
	model := testTUIModel(tuiDependencies{})
	model.static = true
	model.noColor = true
	model.screen = screenRunning
	model.phase = "validating"
	view := model.View()
	if !contains(view, "validating") || contains(view, "\x1b[") || contains(view, "%") {
		t.Fatalf("static progress view should show real phase without style or fake percentage: %q", view)
	}
	model, _ = updateTUI(model, installProgressMsg{event: installEvent{Phase: "backup"}})
	if got := model.phase; got != "backup" {
		t.Fatalf("phase = %q, want backup", got)
	}
}

func runBackendCommand(t *testing.T, command tea.Cmd) tea.Msg {
	t.Helper()
	result := command()
	batch, ok := result.(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("backend command did not return a non-empty batch: %T", result)
	}
	return batch[0]()
}

func TestTUIBatchedNavigationKeys(t *testing.T) {
	model := testTUIModel(tuiDependencies{})
	model, _ = updateTUI(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jj")})
	if model.selected != 2 {
		t.Fatalf("batched j keys selected %d, want backup listing (2)", model.selected)
	}
	model, _ = updateTUI(model, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("kk")})
	if model.selected != 0 {
		t.Fatalf("batched k keys selected %d, want install (0)", model.selected)
	}
}

func updateTUI(model *tuiModel, msg tea.Msg) (*tuiModel, tea.Cmd) {
	updated, cmd := model.Update(msg)
	return updated.(*tuiModel), cmd
}

func testTUIModel(deps tuiDependencies) *tuiModel {
	return newTUIModel(Configuration{}, false, false, nil, "/tmp", deps)
}

func contains(text, part string) bool {
	return strings.Contains(text, part)
}
