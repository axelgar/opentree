package workspace

import (
	"os/exec"
	"time"

	"github.com/axelgar/opentree/pkg/tmux"
)

// TmuxProcessManager adapts a tmux.Controller to the ProcessManager interface.
type TmuxProcessManager struct {
	ctrl *tmux.Controller
}

// NewTmuxProcessManager wraps a tmux.Controller as a ProcessManager.
func NewTmuxProcessManager(ctrl *tmux.Controller) *TmuxProcessManager {
	return &TmuxProcessManager{ctrl: ctrl}
}

func (t *TmuxProcessManager) CreateWindow(name, workdir, command string, args ...string) error {
	return t.ctrl.CreateWindow(name, workdir, command, args...)
}

func (t *TmuxProcessManager) ListWindows() ([]Window, error) {
	tw, err := t.ctrl.ListWindows()
	if err != nil {
		return nil, err
	}
	windows := make([]Window, len(tw))
	for i, w := range tw {
		windows[i] = Window{ID: w.ID, Name: w.Name, Active: w.Active}
	}
	return windows, nil
}

func (t *TmuxProcessManager) SelectWindow(name string) error {
	return t.ctrl.SelectWindow(name)
}

func (t *TmuxProcessManager) AttachWindow(name string) error {
	return t.ctrl.AttachWindow(name)
}

func (t *TmuxProcessManager) AttachCmd(name string) (*exec.Cmd, error) {
	return t.ctrl.AttachCmd(name)
}

func (t *TmuxProcessManager) KillWindow(name string) error {
	return t.ctrl.KillWindow(name)
}

func (t *TmuxProcessManager) KillSession() error {
	return t.ctrl.KillSession()
}

func (t *TmuxProcessManager) CapturePane(name string, lines int) (string, error) {
	return t.ctrl.CapturePane(name, lines)
}

func (t *TmuxProcessManager) GetWindowActivity(name string) (time.Time, error) {
	return t.ctrl.GetWindowActivity(name)
}

func (t *TmuxProcessManager) SendMessage(name, text string) error {
	return t.ctrl.SendMessage(name, text)
}
