package chat

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/axelgar/opentree/pkg/acp"
)

var testConfigOptions = []acp.ConfigOption{
	{
		ID: "model", Name: "Model", Category: "model", Type: "select",
		CurrentValue: "sonnet-4.6",
		Options: []acp.ConfigOptionValue{
			{Value: "haiku-4.5", Name: "Claude Haiku 4.5"},
			{Value: "sonnet-4.6", Name: "Claude Sonnet 4.6"},
			{Value: "gpt-5.4", Name: "GPT-5.4"},
		},
	},
	{
		ID: "effort", Name: "Effort", Category: "thought_level", Type: "select",
		CurrentValue: "low",
		Options: []acp.ConfigOptionValue{
			{Value: "low", Name: "Low"},
			{Value: "high", Name: "High"},
		},
	},
	{
		ID: "mode", Name: "Session Mode", Category: "mode", Type: "select",
		CurrentValue: "build",
		Options: []acp.ConfigOptionValue{
			{Value: "build", Name: "build", Description: "The default agent."},
			{Value: "plan", Name: "plan", Description: "Disallows all edit tools."},
		},
	},
}

func newSettingsModel() Model {
	m := newTestModel()
	m.configOptions = testConfigOptions
	return m
}

func openSettings(m Model) Model {
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	return m
}

func TestSettings_OpensOverDeclaredOptions(t *testing.T) {
	// Nothing here names "model" or "mode": the picker lists whatever the agent
	// declared, so an agent with different controls needs no code change.
	m := openSettings(newSettingsModel())
	if !m.settings.open {
		t.Fatal("ctrl+g should open the settings picker")
	}

	view := m.settingsView()
	for _, want := range []string{"Model", "Effort", "Session Mode", "sonnet-4.6", "build"} {
		if !strings.Contains(view, want) {
			t.Errorf("settingsView() missing %q\ngot:\n%s", want, view)
		}
	}
}

func TestSettings_NoOptionsIsReported(t *testing.T) {
	m := newTestModel() // an agent that declares nothing
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyCtrlG})
	if m.settings.open {
		t.Error("the picker should not open with nothing to pick")
	}
	if m.err == nil {
		t.Error("expected an explanation rather than a picker that does nothing")
	}
}

func TestSettings_DrillsIntoAnOptionAndBackOut(t *testing.T) {
	m := openSettings(newSettingsModel())
	m, _ = applyUpdate(m, keyMsg("enter")) // Model

	if m.settings.configID != "model" {
		t.Fatalf("configID = %q, want model", m.settings.configID)
	}
	if !strings.Contains(m.settingsView(), "Claude Sonnet 4.6") {
		t.Error("expected the model's values to be listed")
	}

	m, _ = applyUpdate(m, keyMsg("esc"))
	if m.settings.configID != "" {
		t.Error("esc should step back to the option list")
	}
	if !m.settings.open {
		t.Error("esc from a value list should not close the picker outright")
	}

	m, _ = applyUpdate(m, keyMsg("esc"))
	if m.settings.open {
		t.Error("a second esc should close the picker")
	}
}

func TestSettings_OpensOnTheCurrentValue(t *testing.T) {
	// sonnet-4.6 is second in the list; landing on the first would make it easy
	// to change a setting by pressing enter twice without meaning to.
	m := openSettings(newSettingsModel())
	m, _ = applyUpdate(m, keyMsg("enter"))
	if m.settings.cursor != 1 {
		t.Errorf("cursor = %d, want the current value (index 1)", m.settings.cursor)
	}
}

func TestSettings_SelectingAValueSendsIt(t *testing.T) {
	m := openSettings(newSettingsModel())
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyDown}) // Effort
	m, _ = applyUpdate(m, keyMsg("enter"))
	if m.settings.configID != "effort" {
		t.Fatalf("configID = %q, want effort", m.settings.configID)
	}

	m, cmd := applyUpdate(m, keyMsg("2")) // High
	if m.settings.open {
		t.Error("choosing a value should close the picker")
	}
	if cmd == nil {
		t.Fatal("expected the change to be sent to the agent")
	}
}

func TestSettings_Navigation(t *testing.T) {
	m := openSettings(newSettingsModel())
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.settings.cursor != len(testConfigOptions)-1 {
		t.Errorf("cursor = %d, want it to wrap to the last option", m.settings.cursor)
	}
	m, _ = applyUpdate(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.settings.cursor != 0 {
		t.Errorf("cursor = %d, want it to wrap back to 0", m.settings.cursor)
	}
}

func TestSettings_TypingDoesNotReachTheInput(t *testing.T) {
	m := openSettings(newSettingsModel())
	m, _ = applyUpdate(m, keyMsg("h"))
	if m.input.Value() != "" {
		t.Errorf("input = %q, want keystrokes consumed by the picker", m.input.Value())
	}
}

func TestConfigChanged_UpdatesStateAndHeader(t *testing.T) {
	m := newSettingsModel()
	updated := []acp.ConfigOption{
		{ID: "model", Name: "Model", Category: "model", CurrentValue: "gpt-5.4"},
		{ID: "mode", Name: "Session Mode", Category: "mode", CurrentValue: "plan"},
	}
	m, _ = applyUpdate(m, configChangedMsg{configID: "model", value: "gpt-5.4", options: updated})

	if !strings.Contains(m.header(), "gpt-5.4") {
		t.Errorf("header = %q, want the new model", m.header())
	}
	last := m.entries[len(m.entries)-1]
	if !strings.Contains(last.text, "model → gpt-5.4") {
		t.Errorf("last entry = %q, want the change recorded", last.text)
	}
}

func TestConfigChanged_ErrorIsSurfaced(t *testing.T) {
	m := newSettingsModel()
	m, _ = applyUpdate(m, configChangedMsg{configID: "model", err: errString("Invalid params")})
	if m.err == nil || !strings.Contains(m.err.Error(), "model") {
		t.Errorf("err = %v, want it to name the setting that failed", m.err)
	}
	// The agent's own values must not be overwritten by a failed change.
	if got := m.settingsSummary(); len(got) == 0 || got[0] != "sonnet-4.6" {
		t.Errorf("summary = %v, want the unchanged model", got)
	}
}

func TestSettingsSummary_OnlyModelAndMode(t *testing.T) {
	// Effort is real but not worth permanent header space.
	got := strings.Join(newSettingsModel().settingsSummary(), " ")
	if !strings.Contains(got, "sonnet-4.6") || !strings.Contains(got, "build") {
		t.Errorf("summary = %q, want model and mode", got)
	}
	if strings.Contains(got, "low") {
		t.Errorf("summary = %q, should not include effort", got)
	}
}

func TestSettings_LongListScrollsAndCounts(t *testing.T) {
	many := make([]acp.ConfigOptionValue, 30)
	for i := range many {
		many[i] = acp.ConfigOptionValue{Value: fmt.Sprint(i), Name: fmt.Sprintf("model-%d", i)}
	}
	m := newTestModel()
	m.configOptions = []acp.ConfigOption{{ID: "model", Name: "Model", CurrentValue: "29", Options: many}}
	m = openSettings(m)
	m, _ = applyUpdate(m, keyMsg("enter"))

	if m.settings.cursor != 29 {
		t.Fatalf("cursor = %d, want the current value at the end of the list", m.settings.cursor)
	}
	view := m.settingsView()
	if !strings.Contains(view, "model-29") {
		t.Error("the selected row should be scrolled into view")
	}
	if strings.Contains(view, "model-0 ") {
		t.Error("rows before the window should be scrolled out")
	}
	if !strings.Contains(view, "30 of 30") {
		t.Errorf("expected a position counter for a list longer than the window\ngot:\n%s", view)
	}
	// The footer must not try to grow to thirty rows.
	if h := m.footerHeight(); h > settingsWindow+8 {
		t.Errorf("footerHeight = %d, want it capped near the window size", h)
	}
}
