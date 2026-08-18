package chat

import (
	"fmt"
	"os"
	"path/filepath"
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

func TestConfigChanged_UpdatesTheFlagsAndLeavesTheLogAlone(t *testing.T) {
	// Flipping a setting must not write to the conversation: the value is on
	// screen permanently, and a line per flip buries the actual conversation.
	m := newSettingsModel()
	before := len(m.entries)
	updated := []acp.ConfigOption{
		{ID: "model", Name: "Model", Category: "model", CurrentValue: "gpt-5.4"},
		{ID: "mode", Name: "Session Mode", Category: "mode", CurrentValue: "plan"},
	}
	m, _ = applyUpdate(m, configChangedMsg{configID: "model", value: "gpt-5.4", options: updated})

	if len(m.entries) != before {
		t.Errorf("entries grew to %d; a settings change should not be logged", len(m.entries))
	}
	if !strings.Contains(m.header(), "gpt-5.4") {
		t.Errorf("header = %q, want the new model", m.header())
	}
	if !strings.Contains(m.statusLine(), "plan") {
		t.Errorf("statusLine = %q, want the new mode shown as a flag", m.statusLine())
	}
}

func TestConfigChanged_ErrorIsSurfaced(t *testing.T) {
	m := newSettingsModel()
	m, _ = applyUpdate(m, configChangedMsg{configID: "model", err: stringError("Invalid params")})
	if m.err == nil || !strings.Contains(m.err.Error(), "model") {
		t.Errorf("err = %v, want it to name the setting that failed", m.err)
	}
	// The agent's own values must not be overwritten by a failed change.
	if got := m.settingsSummary(); len(got) == 0 || got[0] != "sonnet-4.6" {
		t.Errorf("summary = %v, want the unchanged model", got)
	}
}

func TestSummaries_SplitModelFromTheLiveFlags(t *testing.T) {
	m := newSettingsModel()

	header := strings.Join(m.settingsSummary(), " ")
	if header != "sonnet-4.6" {
		t.Errorf("header summary = %q, want just the model", header)
	}

	// Everything else the agent declares becomes a flag, without naming any of
	// them here: mode and effort qualify because opencode declares them.
	flags := strings.Join(m.flagsSummary(), " ")
	for _, want := range []string{"build", "low"} {
		if !strings.Contains(flags, want) {
			t.Errorf("flags = %q, want it to contain %q", flags, want)
		}
	}
	if strings.Contains(flags, "sonnet") {
		t.Errorf("flags = %q, the model belongs in the header", flags)
	}
}

func TestStatusLine_ShowsFlagsBesideTheHelp(t *testing.T) {
	m := newSettingsModel()
	line := m.statusLine()
	for _, want := range []string{"enter", "build", "low"} {
		if !strings.Contains(line, want) {
			t.Errorf("statusLine = %q, want it to contain %q", line, want)
		}
	}
}

func TestStatusLine_FlagsSurviveANarrowTerminal(t *testing.T) {
	// The help gives way, not the flags.
	m := newSettingsModel()
	m.width = 30
	line := m.statusLine()
	if !strings.Contains(line, "build") || !strings.Contains(line, "low") {
		t.Errorf("statusLine = %q, want the flags kept when space is tight", line)
	}
}

func TestStatusLine_NoFlagsWithoutSettings(t *testing.T) {
	m := newTestModel()
	if strings.Contains(m.statusLine(), "·") {
		t.Errorf("statusLine = %q, want no flag section for an agent with no settings", m.statusLine())
	}
}

func TestStatusLine_ErrorKeepsTheFlags(t *testing.T) {
	m := newSettingsModel()
	m.err = stringError("prompt failed")
	line := m.statusLine()
	if !strings.Contains(line, "prompt failed") {
		t.Errorf("statusLine = %q, want the error", line)
	}
	if !strings.Contains(line, "build") {
		t.Errorf("statusLine = %q, want the flags to survive an error", line)
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
	if h := m.footerHeight(); h > pickerWindow+8 {
		t.Errorf("footerHeight = %d, want it capped near the window size", h)
	}
}

// ---------------------------------------------------------------------------
// /model and shift+tab
// ---------------------------------------------------------------------------

func TestClientCommands_DerivedFromDeclaredSettings(t *testing.T) {
	// Nothing names "model" or "mode": each command exists because the agent
	// declared that setting, which is also why /effort appears for free.
	got := map[string]bool{}
	for _, c := range newSettingsModel().clientCommands() {
		got[c.Name] = true
	}
	for _, want := range []string{"model", "effort", "mode"} {
		if !got[want] {
			t.Errorf("/%s missing; commands = %v", want, got)
		}
	}
}

func TestClientCommands_NoneWithoutSettings(t *testing.T) {
	if got := newTestModel().clientCommands(); len(got) != 0 {
		t.Errorf("commands = %v, want none when the agent declares no settings", got)
	}
}

func TestClientCommands_AgentKeepsItsOwnName(t *testing.T) {
	m := newSettingsModel()
	m.commands = []acp.Command{{Name: "model", Description: "the agent's own"}}
	for _, c := range m.clientCommands() {
		if c.Name == "model" {
			t.Error("an agent advertising /model should keep it; its command is more specific")
		}
	}
}

func TestPalette_OffersClientCommands(t *testing.T) {
	m := newSettingsModel()
	m = typeInto(m, "/mod")
	if !m.completion.active() {
		t.Fatal("expected the palette to offer /model")
	}
	if m.completion.items[0].value != "/model" {
		t.Errorf("first item = %q, want /model", m.completion.items[0].value)
	}
	if !strings.Contains(m.completion.items[0].desc, "sonnet-4.6") {
		t.Errorf("desc = %q, want the current value shown", m.completion.items[0].desc)
	}
}

func TestSend_ClientCommandOpensThePickerInsteadOfPrompting(t *testing.T) {
	m := newSettingsModel()
	m.input.SetValue("/model")
	m, cmd := applyUpdate(m, keyMsg("enter"))

	if m.turn {
		t.Fatal("/model must not be sent to the agent as a prompt")
	}
	if cmd != nil {
		t.Error("no prompt should have been issued")
	}
	if !m.settings.open || m.settings.configID != "model" {
		t.Fatalf("settings = %+v, want the model picker open", m.settings)
	}
	// Straight to the values, and landed on the current one.
	if m.settings.cursor != 1 {
		t.Errorf("cursor = %d, want the current model", m.settings.cursor)
	}
	if m.input.Value() != "" {
		t.Errorf("input = %q, want it cleared", m.input.Value())
	}
}

func TestSend_UnknownSlashStillGoesToTheAgent(t *testing.T) {
	// The agent's own commands are prompts; only opentree's are intercepted.
	m := newSettingsModel()
	m.input.SetValue("/code-review")
	m, cmd := applyUpdate(m, keyMsg("enter"))

	if m.settings.open {
		t.Error("an agent command should not open the settings picker")
	}
	if !m.turn || cmd == nil {
		t.Error("expected it to be sent as a prompt")
	}
}

func TestSend_ClientCommandWorksWhileTheAgentIsBusy(t *testing.T) {
	// Changing a setting is not a prompt, so a running turn should not block it.
	m := newSettingsModel()
	m.turn = true
	m.input.SetValue("/mode")
	m, _ = applyUpdate(m, keyMsg("enter"))
	if !m.settings.open {
		t.Error("expected the picker to open mid-turn")
	}
}

func TestNextMode_AdvancesAndWraps(t *testing.T) {
	configID, value, ok := nextMode(testConfigOptions) // currently "build"
	if !ok {
		t.Fatal("expected a mode to cycle")
	}
	if configID != "mode" || value != "plan" {
		t.Errorf("next = %s -> %s, want mode -> plan", configID, value)
	}

	wrapped := []acp.ConfigOption{{
		ID: "mode", Category: "mode", CurrentValue: "plan",
		Options: []acp.ConfigOptionValue{{Value: "build"}, {Value: "plan"}},
	}}
	if _, value, _ := nextMode(wrapped); value != "build" {
		t.Errorf("from the last mode = %q, want it to wrap to build", value)
	}
}

func TestNextMode_NoneDeclared(t *testing.T) {
	if _, _, ok := nextMode(nil); ok {
		t.Error("an agent with no mode has nothing to cycle")
	}
	single := []acp.ConfigOption{{ID: "mode", Category: "mode", CurrentValue: "build",
		Options: []acp.ConfigOptionValue{{Value: "build"}}}}
	if _, _, ok := nextMode(single); ok {
		t.Error("a single mode is not worth cycling")
	}
}

func TestCycleMode_ShiftTabIssuesTheChange(t *testing.T) {
	if _, cmd := applyUpdate(newSettingsModel(), tea.KeyMsg{Type: tea.KeyShiftTab}); cmd == nil {
		t.Error("shift+tab should issue a mode change")
	}
}

func TestCycleMode_NoModeIsANoop(t *testing.T) {
	m := newTestModel() // no declared settings at all
	if _, cmd := applyUpdate(m, tea.KeyMsg{Type: tea.KeyShiftTab}); cmd != nil {
		t.Error("shift+tab should do nothing when the agent declares no mode")
	}
}

// TestFlagsSummary_OnlyTheSettingsWorthPermanentSpace is the Claude Code
// regression: it declares five options, and showing every non-model one filled
// the line with "auto · high · off · default", pushing the help off screen to
// report two things nobody changes mid-conversation.
func TestFlagsSummary_OnlyTheSettingsWorthPermanentSpace(t *testing.T) {
	claudeLike := []acp.ConfigOption{
		{ID: "mode", Category: "mode", CurrentValue: "auto"},
		{ID: "model", Category: "model", CurrentValue: "opus[1m]"},
		{ID: "effort", Category: "thought_level", CurrentValue: "high"},
		{ID: "fast", Category: "model_config", CurrentValue: "off"},
		{ID: "agent", CurrentValue: "default"},
	}
	m := newTestModel()
	m.configOptions = claudeLike

	got := m.flagsSummary()
	if strings.Join(got, " · ") != "auto · high" {
		t.Errorf("flags = %v, want [auto high] — mode first, then effort", got)
	}
	// The ones left out are still reachable.
	names := map[string]bool{}
	for _, c := range m.clientCommands() {
		names[c.Name] = true
	}
	for _, want := range []string{"fast", "agent"} {
		if !names[want] {
			t.Errorf("/%s should still exist; a setting without a flag is not a hidden setting", want)
		}
	}
}

// A value is a wire id, and ACP's own spelling for a well-known mode is a URL.
// Copilot uses it, and the flag beside the input is no place for one.
func TestFlagsSummary_ShowsTheNameNotTheWireValue(t *testing.T) {
	const agentMode = "https://agentclientprotocol.com/protocol/session-modes#agent"
	m := newTestModel()
	m.configOptions = []acp.ConfigOption{
		{ID: "mode", Category: "mode", CurrentValue: agentMode, Options: []acp.ConfigOptionValue{
			{Value: agentMode, Name: "Agent"},
			{Value: "https://agentclientprotocol.com/protocol/session-modes#plan", Name: "Plan"},
		}},
		{ID: "reasoning_effort", Category: "thought_level", CurrentValue: "medium"},
	}
	if got := m.flagsSummary(); strings.Join(got, " · ") != "Agent · medium" {
		t.Errorf("flags = %v, want [Agent medium] — the name the agent gave the mode, not its id", got)
	}

	// A value the agent never listed is all there is to show.
	m.configOptions = []acp.ConfigOption{
		{ID: "mode", Category: "mode", CurrentValue: "build", Options: []acp.ConfigOptionValue{
			{Value: "plan", Name: "Plan"},
		}},
	}
	if got := m.flagsSummary(); len(got) != 1 || got[0] != "build" {
		t.Errorf("flags = %v, want the raw value back when no option matches it", got)
	}
}

func TestFlagsSummary_ModeLeadsRegardlessOfDeclarationOrder(t *testing.T) {
	m := newTestModel()
	m.configOptions = []acp.ConfigOption{
		{ID: "effort", Category: "thought_level", CurrentValue: "low"},
		{ID: "mode", Category: "mode", CurrentValue: "plan"},
	}
	if got := m.flagsSummary(); strings.Join(got, " ") != "plan low" {
		t.Errorf("flags = %v, want mode first — it is the one shift+tab cycles", got)
	}
}

func TestFlagsSummary_PartialDeclarations(t *testing.T) {
	m := newTestModel()
	m.configOptions = []acp.ConfigOption{{ID: "mode", Category: "mode", CurrentValue: "build"}}
	if got := m.flagsSummary(); len(got) != 1 || got[0] != "build" {
		t.Errorf("flags = %v, want just the mode when that is all there is", got)
	}
}

// ---------------------------------------------------------------------------
// Missing adapter
// ---------------------------------------------------------------------------

// newUnlaunchedModel is the state Run leaves behind when the very first spawn
// fails — for an agent reached through an adapter, what a missing adapter looks
// like. It used to be a bare CLI error printed into a tmux window that then
// closed, leaving nothing to act on.
func newUnlaunchedModel() Model {
	m := newTestModel()
	m.client = nil
	m.dead = true
	m.err = stringError("failed to start claude-agent-acp: executable file not found in $PATH")
	// The one registry agent behind an adapter, so the panel has an install
	// hint to derive — pointed at a name nothing can resolve, because whether
	// the machine running these tests happens to have the real adapter sitting
	// on its PATH is not what any of them is about.
	m.opts.Agent = testAgent("claude")
	m.opts.Agent.ACP.Command = "opentree-adapter-that-is-not-installed"
	return m
}

func TestMissingAdapter_SaysWhereToGetIt(t *testing.T) {
	m := newUnlaunchedModel()
	if !m.stopped() {
		t.Fatal("a failed launch should leave the view stopped, not closed")
	}
	view := m.footer()
	// The chat states the problem and points at the agent list; installing a
	// 340MB dependency from inside a conversation that cannot start reads as
	// the wrong place for it.
	for _, want := range []string{"340MB", "agent list", "[r]"} {
		if !strings.Contains(view, want) {
			t.Errorf("stopped panel missing %q\ngot:\n%s", want, view)
		}
	}
}

// An agent that started and later died wants a restart, not advice about a
// dependency it demonstrably has.
//
// Whether the adapter is on the machine is what tells the two apart. The panel
// used to ask whether this chat had ever held a client, which is false of every
// failure there is — an adapter that crashes on launch, one that accepts stdio
// and never answers the handshake, a proxy between it and its API — and all of
// them were told to go and install what was already sitting on the PATH, in
// place of the line that would have helped.
func TestRunningAgent_IsNotToldToInstallAnything(t *testing.T) {
	m := newUnlaunchedModel()
	// sh stands in for an adapter that is plainly installed; opentree needs a
	// POSIX shell to run tmux at all, so it is there.
	m.opts.Agent.ACP.Command = "sh"
	m.err = stringError("claude-agent-acp exited")
	m.client = &acp.Client{} // it launched once
	m, _ = applyUpdate(m, agentGoneMsg{generation: m.generation})

	if strings.Contains(m.footer(), "agent list") {
		t.Errorf("stopped panel should not mention installing\ngot:\n%s", m.footer())
	}
}

// And the same agent with the adapter genuinely absent still gets the hint,
// whether or not a client was ever held — a crash before the first handshake
// and a missing binary look identical from the outside.
func TestMissingAdapter_HintSurvivesAClientThatOnceExisted(t *testing.T) {
	m := newUnlaunchedModel()
	m.client = &acp.Client{}
	m, _ = applyUpdate(m, agentGoneMsg{generation: m.generation})

	if !strings.Contains(m.footer(), "agent list") {
		t.Errorf("an adapter that is not installed was not offered the install\ngot:\n%s", m.footer())
	}
}

func TestAcpBinary_ResolvedPerLaunch(t *testing.T) {
	// Installing the adapter moves it, so the path is resolved on every launch
	// rather than once at startup — which is why pressing install and then
	// restart kept failing until the process was restarted.
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := Options{Agent: testAgent("claude")}
	if got := o.acpBinary(); got != "claude-agent-acp" {
		t.Errorf("acpBinary() = %q, want the bare name before the install", got)
	}
	managed := filepath.Join(home, ".opentree", "tools", "bin", "claude-agent-acp")
	if err := os.MkdirAll(filepath.Dir(managed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := o.acpBinary(); got != managed {
		t.Errorf("after the install acpBinary() = %q, want the freshly resolved %q", got, managed)
	}
}

func TestAcpBinary_FallsBackToTheAgentItself(t *testing.T) {
	// opencode serves ACP directly; there is no separate binary to resolve.
	t.Setenv("HOME", t.TempDir())
	o := Options{Agent: testAgent("opencode")}
	if got := o.acpBinary(); got != "opencode" {
		t.Errorf("acpBinary() = %q, want the agent's own binary", got)
	}
}

func TestAuthUsesTheAgentNotTheAdapter(t *testing.T) {
	// `claude-agent-acp auth login` is not a thing; the login belongs to the
	// agent's own binary.
	m := newUnlaunchedModel()
	m.authNeed = true

	if !strings.Contains(m.footer(), "claude auth login") {
		t.Errorf("stopped panel should offer the agent's own login\ngot:\n%s", m.footer())
	}
	if strings.Contains(m.footer(), "claude-agent-acp auth") {
		t.Error("the adapter has no auth subcommand")
	}
}

func TestUnlaunchedModel_DoesNotStartASession(t *testing.T) {
	// startSession would dereference a nil client.
	if cmd := newUnlaunchedModel().startSession(); cmd != nil {
		t.Error("there is no client to open a session on")
	}
}
