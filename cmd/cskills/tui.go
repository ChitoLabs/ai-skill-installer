package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type tuiScreen uint8

const (
	screenMenu tuiScreen = iota
	screenConfirm
	screenRunning
	screenResult
)

type confirmChoice uint8

const (
	confirmCancel confirmChoice = iota
	confirmProceed
)

type tuiDependencies struct {
	install func(context.Context, Configuration, bool, map[string]string, io.Writer, func(installEvent)) error
	status  func(io.Writer, map[string]string, string) error
	list    func(io.Writer, io.Writer, map[string]string, string) error
	emit    func(tea.Msg)
}

type tuiModel struct {
	ctx             context.Context
	config          Configuration
	environment     map[string]string
	scriptDir       string
	deps            tuiDependencies
	screen          tuiScreen
	selected        int
	confirm         confirmChoice
	phase           string
	atomic          bool
	static          bool
	noColor         bool
	operation       *tuiOperation
	cancelRequested bool
	spinnerFrame    int
	result          string
	resultErr       error
	resultWarnings  string
}

type installProgressMsg struct{ event installEvent }
type installFinishedMsg struct {
	output string
	err    error
}
type taskFinishedMsg struct {
	output   string
	warnings string
	err      error
}
type externalCancelMsg struct{}
type spinnerTickMsg struct{}

type tuiOperation struct {
	mu       sync.Mutex
	started  bool
	stopping bool
	finished bool
	done     chan struct{}
	cancel   context.CancelFunc
}

func newTUIOperation(cancel context.CancelFunc) *tuiOperation {
	return &tuiOperation{done: make(chan struct{}), cancel: cancel}
}

func (operation *tuiOperation) begin() bool {
	operation.mu.Lock()
	defer operation.mu.Unlock()
	if operation.stopping {
		return false
	}
	operation.started = true
	return true
}

func (operation *tuiOperation) finish() {
	operation.mu.Lock()
	if !operation.finished {
		operation.finished = true
		close(operation.done)
	}
	operation.mu.Unlock()
}

func (operation *tuiOperation) cancelPreparation() {
	operation.mu.Lock()
	cancel := operation.cancel
	operation.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (operation *tuiOperation) stopAndWait() {
	operation.mu.Lock()
	operation.stopping = true
	started, finished, cancel := operation.started, operation.finished, operation.cancel
	operation.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if started && !finished {
		<-operation.done
	}
}

var menuOptions = []string{"Install / update", "Status", "List backups", "Exit"}
var spinnerFrames = []string{"|", "/", "-", "\\"}

func newTUIModel(cfg Configuration, noColor, static bool, environment map[string]string, scriptDir string, deps tuiDependencies) *tuiModel {
	if deps.install == nil {
		deps.install = runInstallWithProgress
	}
	if deps.status == nil {
		deps.status = runStatusCommand
	}
	if deps.list == nil {
		deps.list = runListCommand
	}
	return &tuiModel{
		ctx: context.Background(), config: cfg, noColor: noColor, static: static,
		environment: environment, scriptDir: scriptDir, deps: deps, screen: screenMenu,
	}
}

func runInteractiveMenu(ctx context.Context, out io.Writer, noColor, static bool, environment map[string]string, scriptDir string) error {
	menuCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	model := newTUIModel(Configuration{}, noColor, static, environment, scriptDir, tuiDependencies{})
	model.ctx = menuCtx
	program := tea.NewProgram(model, tea.WithInput(os.Stdin), tea.WithOutput(out), tea.WithoutSignalHandler())
	model.deps.emit = program.Send
	return runTUIRuntime(model, func() (tea.Model, error) {
		finalModel, err := program.Run()
		cancel()
		return finalModel, err
	})
}

func waitForTUIOperation(model *tuiModel) {
	if model.operation != nil {
		model.operation.stopAndWait()
	}
}

func runTUIRuntime(model *tuiModel, run func() (tea.Model, error)) error {
	_, err := run()
	waitForTUIOperation(model)
	return err
}

func (m *tuiModel) Init() tea.Cmd {
	return func() tea.Msg {
		if m.ctx == nil {
			return nil
		}
		<-m.ctx.Done()
		return externalCancelMsg{}
	}
}

func (m *tuiModel) spinnerCmd() tea.Cmd {
	if m.static || m.noColor {
		return nil
	}
	return tea.Tick(100*time.Millisecond, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

func (m *tuiModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case externalCancelMsg:
		if m.screen == screenRunning {
			return m.requestCancel()
		}
		return m, tea.Quit
	case spinnerTickMsg:
		if m.screen == screenRunning && !m.static && !m.noColor {
			m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
			return m, m.spinnerCmd()
		}
		return m, nil
	case installProgressMsg:
		m.phase = msg.event.Phase
		m.atomic = msg.event.Atomic
		return m, nil
	case installFinishedMsg:
		m.operation = nil
		m.result = msg.output
		m.resultErr = msg.err
		m.screen = screenResult
		return m, nil
	case taskFinishedMsg:
		m.operation = nil
		m.result = msg.output
		m.resultWarnings = msg.warnings
		m.resultErr = msg.err
		m.screen = screenResult
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	}
	return m, nil
}

func (m *tuiModel) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.screen == screenRunning {
		switch key.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m.requestCancel()
		case tea.KeyRunes:
			if len(key.Runes) == 1 && (key.Runes[0] == 'q' || key.Runes[0] == 'Q') {
				return m.requestCancel()
			}
		}
		return m, nil
	}
	switch key.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyRunes:
		// A single terminal read may batch repeated navigation keys.
		for _, key := range key.Runes {
			switch key {
			case 'q', 'Q':
				return m, tea.Quit
			case 'j':
				m.navigateDown()
			case 'k':
				m.navigateUp()
			}
		}
	case tea.KeyUp, tea.KeyLeft:
		m.navigateUp()
	case tea.KeyDown, tea.KeyRight:
		m.navigateDown()
	case tea.KeyEnter:
		if m.screen == screenConfirm {
			if m.confirm == confirmProceed {
				return m.startInstall()
			}
			m.screen = screenMenu
			return m, nil
		}
		if m.screen == screenResult {
			m.screen = screenMenu
			m.result, m.resultErr, m.resultWarnings = "", nil, ""
			return m, nil
		}
		if m.screen == screenMenu {
			return m.activateSelection()
		}
	}
	return m, nil
}

func (m *tuiModel) navigateUp() {
	if m.screen == screenMenu && m.selected > 0 {
		m.selected--
	} else if m.screen == screenConfirm {
		m.confirm = confirmCancel
	}
}

func (m *tuiModel) navigateDown() {
	if m.screen == screenMenu && m.selected < len(menuOptions)-1 {
		m.selected++
	} else if m.screen == screenConfirm {
		m.confirm = confirmProceed
	}
}

func (m *tuiModel) activateSelection() (tea.Model, tea.Cmd) {
	switch m.selected {
	case 0:
		if m.ctx != nil && m.ctx.Err() != nil {
			m.resultErr, m.screen = m.ctx.Err(), screenResult
			return m, nil
		}
		if m.config.OpenCodeTarget == "" || m.config.ClaudeTarget == "" || m.config.AgyTarget == "" || m.config.AgentsTarget == "" {
			cfg, err := resolveCommandConfiguration(m.environment, m.scriptDir)
			if err != nil {
				m.resultErr, m.screen = err, screenResult
				return m, nil
			}
			m.config = cfg
		}
		m.confirm = confirmCancel
		m.screen = screenConfirm
	case 1:
		status, environment, scriptDir := m.deps.status, cloneTUIEnvironment(m.environment), m.scriptDir
		return m.startTask(func(out, _ io.Writer) error { return status(out, environment, scriptDir) })
	case 2:
		list, environment, scriptDir := m.deps.list, cloneTUIEnvironment(m.environment), m.scriptDir
		return m.startTask(func(out, warnings io.Writer) error { return list(out, warnings, environment, scriptDir) })
	case 3:
		return m, tea.Quit
	}
	return m, nil
}

func cloneTUIEnvironment(environment map[string]string) map[string]string {
	copy := make(map[string]string, len(environment))
	for key, value := range environment {
		copy[key] = value
	}
	return copy
}

func (m *tuiModel) startInstall() (tea.Model, tea.Cmd) {
	if m.ctx != nil && m.ctx.Err() != nil {
		m.resultErr, m.screen = m.ctx.Err(), screenResult
		return m, nil
	}
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	operation := newTUIOperation(cancel)
	m.operation = operation
	install, config, environment, emit := m.deps.install, m.config, cloneTUIEnvironment(m.environment), m.deps.emit
	m.phase, m.atomic, m.cancelRequested, m.spinnerFrame = "starting", false, false, 0
	m.screen = screenRunning
	work := func() tea.Msg {
		if !operation.begin() {
			cancel()
			return installFinishedMsg{err: context.Canceled}
		}
		defer operation.finish()
		defer cancel()
		var output bytes.Buffer
		err := install(ctx, config, false, environment, &output, func(event installEvent) {
			if emit != nil {
				emit(installProgressMsg{event: event})
			}
		})
		return installFinishedMsg{output: output.String(), err: err}
	}
	return m, tea.Batch(work, m.spinnerCmd())
}

func (m *tuiModel) startTask(task func(io.Writer, io.Writer) error) (tea.Model, tea.Cmd) {
	operation := newTUIOperation(nil)
	m.operation = operation
	m.screen = screenRunning
	m.phase, m.cancelRequested, m.spinnerFrame = "working", false, 0
	work := func() tea.Msg {
		if !operation.begin() {
			return taskFinishedMsg{err: context.Canceled}
		}
		defer operation.finish()
		var output, warnings bytes.Buffer
		err := task(&output, &warnings)
		return taskFinishedMsg{output: output.String(), warnings: warnings.String(), err: err}
	}
	return m, tea.Batch(work, m.spinnerCmd())
}

func (m *tuiModel) requestCancel() (tea.Model, tea.Cmd) {
	if m.screen != screenRunning {
		return m, nil
	}
	m.cancelRequested = true
	if !m.atomic && m.operation != nil {
		m.operation.cancelPreparation()
	}
	return m, nil
}

func (m *tuiModel) View() string {
	style := func(value string, color lipgloss.Color) string {
		if m.noColor || m.static {
			return value
		}
		return lipgloss.NewStyle().Foreground(color).Render(value)
	}
	var view strings.Builder
	view.WriteString(style("C-Skills", lipgloss.Color("201")) + "  " + style("INSTALLER", lipgloss.Color("51")) + "\n\n")
	switch m.screen {
	case screenMenu:
		view.WriteString("Choose an action:\n")
		for index, option := range menuOptions {
			marker := "  "
			if index == m.selected {
				marker = "> "
			}
			view.WriteString(marker + option + "\n")
		}
		view.WriteString("\n↑/↓ or j/k navigate · Enter select · q exit")
	case screenConfirm:
		view.WriteString("Confirm full replacement of these targets:\n")
		for _, target := range []string{m.config.OpenCodeTarget, m.config.ClaudeTarget, m.config.AgyTarget, m.config.AgentsTarget} {
			view.WriteString("  " + target + "\n")
		}
		view.WriteString("\nThis will replace the full contents of each target. Continue?\n")
		for index, choice := range []string{"Cancel", "Replace all targets"} {
			marker := "  "
			if confirmChoice(index) == m.confirm {
				marker = "> "
			}
			view.WriteString(marker + choice + "\n")
		}
		view.WriteString("\n↑/↓ or j/k choose · Enter confirm")
	case screenRunning:
		phase := m.phase
		if phase == "" {
			phase = "working"
		}
		if !m.static && !m.noColor {
			view.WriteString("[" + spinnerFrames[m.spinnerFrame%len(spinnerFrames)] + "] ")
		}
		view.WriteString("Installer: " + phase + "\n")
		if m.atomic {
			view.WriteString("Finishing the safe transaction; cancellation is deferred.\n")
		} else if m.cancelRequested {
			view.WriteString("Cancelling preparation; waiting for a safe stop.\n")
		} else {
			view.WriteString("q / Esc / Ctrl+C cancels and waits for a safe stop.\n")
		}
	case screenResult:
		if m.result != "" {
			view.WriteString(m.result)
			if !strings.HasSuffix(m.result, "\n") {
				view.WriteByte('\n')
			}
		}
		if m.resultWarnings != "" {
			view.WriteString(m.resultWarnings)
			if !strings.HasSuffix(m.resultWarnings, "\n") {
				view.WriteByte('\n')
			}
		}
		if m.resultErr != nil {
			view.WriteString("Error: " + m.resultErr.Error() + "\n")
		} else {
			view.WriteString("Complete.\n")
		}
		view.WriteString("\nEnter return to menu · q exit")
	}
	return view.String()
}

var _ tea.Model = (*tuiModel)(nil)
