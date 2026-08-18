package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/bootstrap"
	"github.com/axelgar/opentree/pkg/ui"
)

// A worktree is not ready to work in the moment git makes it. The project's
// setup commands run here, as the chat's first phase, before it connects to the
// agent — this is where the screen and the status socket already are, so the
// output streams into a window that exists and the workspace list can say
// "setting up" without anything new to poll.
//
// The agent starts afterwards, into a worktree that installs and builds. An
// agent that starts before that burns its first turn rediscovering it, and may
// "fix" it by editing the lockfile.

// Setup is the bootstrap phase the caller wants run, or the zero value for a
// chat with nothing to do — a project that configures none, or a worktree that
// already ran these exact commands.
//
// The decisions that need the repository — is this hash already recorded, has
// this machine approved these commands — are made by the caller and arrive
// here as answers. It keeps this view able to run a setup phase without knowing
// what a workspace or a trust file is.
type Setup struct {
	// Commands is what to run, in order. Empty means there is nothing to do.
	Commands []string

	// Run is the dev server command. Nothing here starts it — servers start on
	// demand, from the dashboard — but approving these commands approves it
	// too, so the panel that asks has to show it. Gating only setup would move
	// a payload one key down.
	Run string

	// Trusted is whether this machine has already approved these commands.
	// False puts the approval question on screen before anything runs.
	Trusted bool

	// Approve records approval for next time, once the user has given it here.
	Approve func() error

	// Record marks the workspace as set up, so the next chat in this worktree —
	// and there are many, since losing a window relaunches one — skips straight
	// to the agent.
	Record func() error
}

// wanted reports whether there is a setup phase to run at all.
func (s Setup) wanted() bool { return len(s.Commands) > 0 }

// setupStage is where the phase has got to.
type setupStage int

const (
	setupNone      setupStage = iota // nothing to do, or done with
	setupAsking                      // waiting for this machine to approve the commands
	setupRunning                     // commands are running, output streaming into the log
	setupFailed                      // a command failed, or the user cancelled
	setupLaunching                   // setup is over; the agent is starting
)

// setupPhase is the chat's own state for the phase.
type setupPhase struct {
	stage setupStage
	spec  Setup

	// at is the index of the command being run, for "2 of 3".
	at int

	// cancel stops the running command's whole process group. Held here because
	// the key that uses it arrives long after the command was started.
	cancel context.CancelFunc

	err error
}

// active reports whether the phase owns the screen: nothing else can happen
// until it is answered, finished, or given up on.
func (s setupPhase) active() bool {
	return s.stage == setupAsking || s.stage == setupRunning || s.stage == setupFailed
}

// ---------------------------------------------------------------------------
// Messages
// ---------------------------------------------------------------------------

// setupOutputMsg is one line a setup command printed. It travels down the same
// channel as the agent's own notifications, so the log fills in as it happens
// rather than in one block at the end.
type setupOutputMsg struct{ line string }

// setupStepMsg is the phase moving to the next command, for the panel's count.
type setupStepMsg struct{ at int }

// setupDoneMsg ends the phase, successfully or not.
type setupDoneMsg struct{ err error }

// ---------------------------------------------------------------------------
// Driving the phase
// ---------------------------------------------------------------------------

// beginSetup is the phase's opening state: the approval question when this
// machine has not seen these commands before, and the commands themselves when
// it has.
func (m Model) beginSetup() (Model, tea.Cmd) {
	if !m.setup.spec.wanted() {
		return m, nil
	}
	if !m.setup.spec.Trusted {
		m.setup.stage = setupAsking
		return m.relayout(), nil
	}
	return m.startSetup()
}

// startSetup runs the commands, streaming their output into the log.
func (m Model) startSetup() (Model, tea.Cmd) {
	ctx, cancel := context.WithCancel(m.ctx)
	m.setup.stage = setupRunning
	m.setup.at = 0
	m.setup.err = nil
	m.setup.cancel = cancel

	send, cwd, commands := m.send, m.opts.Cwd, m.setup.spec.Commands
	run := func() tea.Msg {
		step := 0
		err := bootstrap.RunSetup(ctx, cwd, commands, func(line string) {
			// The echoed command is also the marker for a new step: RunSetup
			// announces each one before running it, and the panel's count and
			// the log agree because they come from the same event.
			if strings.HasPrefix(line, "$ ") {
				send(setupStepMsg{at: step})
				step++
			}
			send(setupOutputMsg{line: line})
		})
		cancel()
		return setupDoneMsg{err: err}
	}

	// The spinner has to be started as well as drawn. The tick chain sustains
	// itself but has to be begun by whoever needs it, and until now the only
	// place that did was a turn — so the panel saying "setting up…" sat on frame
	// zero for the whole of an install that can run for minutes, which reads as
	// a window that has hung rather than one that is working.
	m, tick := m.spin()
	return m.relayout(), tea.Batch(run, tick)
}

// finishSetup records a completed phase and hands the window to the agent.
//
// A failure stops here instead: an agent let loose on a half-installed worktree
// spends its first turn on a problem that has nothing to do with the task, and
// may "fix" it by editing the lockfile. The panel offers the two answers worth
// having — try again, or start anyway.
func (m Model) finishSetup(err error) (Model, tea.Cmd) {
	m.setup.cancel = nil
	if err != nil {
		m.setup.stage, m.setup.err = setupFailed, err
		return m.relayout(), nil
	}
	if record := m.setup.spec.Record; record != nil {
		if rerr := record(); rerr != nil {
			// Not a failure of the setup itself: the worktree is ready, and the
			// only cost is that the next chat here runs the commands again.
			m = m.appendNotice("setup finished, but recording it failed: " + rerr.Error())
		}
	}
	return m.launchAfterSetup()
}

// launchAfterSetup starts the agent the phase was holding back.
func (m Model) launchAfterSetup() (Model, tea.Cmd) {
	m.setup.stage = setupLaunching
	return m.relayout(), m.launchCmd(true)
}

// setupSummary is the line the log keeps once the phase is over. The output
// above it is the detail; this is what a reader scrolling past needs.
func setupSummary(commands []string, err error) string {
	switch {
	case err != nil:
		return "setup failed: " + err.Error()
	case len(commands) == 1:
		return "setup finished"
	}
	return fmt.Sprintf("setup finished · %d commands", len(commands))
}

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

// handleSetupKey drives whichever of the phase's three panels is up. It takes
// the whole keyboard while it does: the input box is useless with no agent to
// send to, and these keys would otherwise be typed into it.
func (m Model) handleSetupKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if key.Matches(msg, m.keys.Back) {
		return m, leave
	}

	switch m.setup.stage {
	case setupAsking:
		switch {
		case key.Matches(msg, m.keys.Approve):
			// Recorded before the commands run, not after: what is being
			// approved is this text, and whether it succeeds says nothing about
			// whether the user meant to allow it.
			if approve := m.setup.spec.Approve; approve != nil {
				if err := approve(); err != nil {
					m = m.appendNotice("could not record the approval: " + err.Error())
				}
			}
			return m.startSetup()

		case key.Matches(msg, m.keys.Decline):
			// Not recorded, so the question comes back next time. A refusal is
			// "not now"; the way to make it permanent is to take the commands
			// out of opentree.toml.
			m = m.appendNotice("setup skipped — the worktree may not be ready")
			return m.launchAfterSetup()
		}

	case setupRunning:
		if key.Matches(msg, m.keys.Cancel) && m.setup.cancel != nil {
			m.setup.cancel()
			// No stage change here: the command is being killed, and the phase
			// ends when it actually stops.
			return m, nil
		}

	case setupFailed:
		switch {
		case key.Matches(msg, m.keys.Restart):
			return m.startSetup()
		case key.Matches(msg, m.keys.StartAnyway):
			return m.launchAfterSetup()
		}
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// View
// ---------------------------------------------------------------------------

// setupLines is the panel's body: what is happening, and what can be done
// about it.
func (m Model) setupLines() []string {
	width := max(m.width-6, 20)

	switch m.setup.stage {
	case setupAsking:
		lines := []string{permLabelStyle.Render("This repository asks to run these commands before the agent starts:")}
		for _, c := range m.setup.spec.Commands {
			lines = append(lines, noticeStyle.Render(ui.Truncate("  setup  "+c, width)))
		}
		if run := m.setup.spec.Run; run != "" {
			// Shown because approving covers it: the dev server command is code
			// from the same tracked file, and an approval that mentioned only
			// the install would understate what it allows.
			lines = append(lines, noticeStyle.Render(ui.Truncate("  run    "+run, width)))
		}
		lines = append(lines, helpStyle.Render("from opentree.toml, which arrives with a clone"))
		return append(lines, strings.Join([]string{
			permKeyStyle.Render("[a]") + " " + permLabelStyle.Render("run them"),
			permKeyStyle.Render("[d]") + " " + permLabelStyle.Render("skip this time"),
			permKeyStyle.Render("[ctrl+c]") + " " + permLabelStyle.Render("back to opentree"),
		}, "   "))

	case setupRunning:
		head := fmt.Sprintf("%s setting up… %s",
			ui.SpinnerFrames[m.spinnerFrame], m.setupStepLabel())
		return []string{
			toolRunningStyle.Render(ui.Truncate(head, width)),
			permKeyStyle.Render("[esc]") + " " + permLabelStyle.Render("cancel"),
		}

	default:
		return []string{
			errorStyle.Render(ui.Truncate("✕ "+setupErrorText(m.setup.err), width)),
			strings.Join([]string{
				permKeyStyle.Render("[r]") + " " + permLabelStyle.Render("try again"),
				permKeyStyle.Render("[s]") + " " + permLabelStyle.Render("start the agent anyway"),
				permKeyStyle.Render("[ctrl+c]") + " " + permLabelStyle.Render("back to opentree"),
			}, "   "),
		}
	}
}

// setupStepLabel is which command is running, counted for a phase that has
// more than one. A single command is named rather than numbered: "1 of 1" is
// arithmetic nobody needed.
func (m Model) setupStepLabel() string {
	commands := m.setup.spec.Commands
	if m.setup.at >= len(commands) {
		return ""
	}
	if len(commands) == 1 {
		return commands[0]
	}
	return fmt.Sprintf("(%d of %d) %s", m.setup.at+1, len(commands), commands[m.setup.at])
}

// setupErrorText is the failure in one line. A cancelled command says so in
// words rather than reporting the signal it died of.
func setupErrorText(err error) string {
	if err == nil {
		return "setup did not finish"
	}
	if strings.Contains(err.Error(), context.Canceled.Error()) {
		return "setup cancelled"
	}
	return strings.SplitN(err.Error(), "\n", 2)[0]
}

func (m Model) setupView() string {
	box := permBoxStyle
	hint := "opentree.toml asks to run this"
	switch m.setup.stage {
	case setupRunning:
		hint = "the agent starts when this finishes"
	case setupFailed:
		box = errorBoxStyle
		hint = "the worktree may not be ready to work in"
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(box.Render(strings.Join(m.setupLines(), "\n")))
	b.WriteString("\n")
	b.WriteString(helpStyle.Render(hint))
	return b.String()
}

// setupHeight is the footer space the panel needs: its lines, the box around
// them, and the hint under it.
func (m Model) setupHeight() int { return len(m.setupLines()) + 4 }
