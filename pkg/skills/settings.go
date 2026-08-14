package skills

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/axelgar/opentree/pkg/config"
)

// Installed is not the same as available. An agent can be told to ignore a
// skill sitting right there on disk, and a skill can ask not to be invoked
// unless the user asks for it by name — so a list of directories is only half
// the answer to "what can this agent do".

// State is how available a skill is to one agent. The values are Claude Code's
// own, taken from the schema its binary carries for the overrides map.
type State string

const (
	// StateOn is the default: the agent loads it and may invoke it itself.
	StateOn State = "on"
	// StateNameOnly lists the skill without its description.
	StateNameOnly State = "name-only"
	// StateManualOnly loads the skill but leaves invoking it to the user.
	StateManualOnly State = "user-invocable-only"
	// StateOff means the agent does not load it at all.
	StateOff State = "off"
)

// Label is the short word for a row. On is the unremarkable case and says
// nothing, so a clean list stays clean.
func (s State) Label() string {
	switch s {
	case StateOff:
		return "off"
	case StateManualOnly:
		return "manual"
	case StateNameOnly:
		return "name only"
	}
	return ""
}

// settingsPath resolves one entry of SettingsFiles. A "~/" prefix is the user's
// own settings and an absolute path is taken as given; anything else belongs to
// the repository, and resolves to nothing when there is no repository.
func settingsPath(entry, repoRoot string) string {
	if strings.HasPrefix(entry, "~/") {
		return ExpandUserDir(entry)
	}
	if filepath.IsAbs(entry) {
		return entry
	}
	if repoRoot == "" {
		return ""
	}
	return filepath.Join(repoRoot, entry)
}

// readOverrides layers an agent's settings files into one map of skill name to
// state. Later files win, which is the order the agent itself resolves them in.
func readOverrides(spec config.SkillsSpec, repoRoot string) map[string]State {
	if spec.OverridesKey == "" && spec.DisabledKey == "" {
		return nil
	}
	out := map[string]State{}
	for _, entry := range spec.SettingsFiles {
		path := settingsPath(entry, repoRoot)
		if path == "" {
			continue
		}
		data, err := os.ReadFile(path) // #nosec G304 -- a settings path from the agent registry
		if err != nil {
			continue // an absent settings file is the common case
		}
		var doc map[string]json.RawMessage
		if err := json.Unmarshal(data, &doc); err != nil {
			continue // a settings file opentree cannot read is the agent's problem
		}
		var overrides map[string]State
		if err := json.Unmarshal(doc[spec.OverridesKey], &overrides); err == nil {
			for name, state := range overrides {
				out[name] = state
			}
		}
		// No later-wins here: a name missing from a list of the disabled says
		// nothing, so a skill switched off in either file stays off.
		for _, name := range disabledNames(doc, spec.DisabledKey) {
			out[name] = StateOff
		}
	}
	return out
}

// disabledNames reads a list of skill names from a dotted path through a
// settings document — "skills.disabled" is Gemini's, and it keeps one at each
// scope.
func disabledNames(doc map[string]json.RawMessage, key string) []string {
	if key == "" {
		return nil
	}
	parts := strings.Split(key, ".")
	raw, ok := doc[parts[0]]
	if !ok {
		return nil
	}
	for _, part := range parts[1:] {
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(raw, &nested); err != nil {
			return nil
		}
		if raw, ok = nested[part]; !ok {
			return nil
		}
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil {
		return nil
	}
	return names
}

// OverrideFile is where a change to a skill's state should be written: the file
// already carrying an override for it, and otherwise the first one the agent
// reads.
//
// Preferring the file that already holds the override is what makes the write
// take effect. Writing "on" into a higher-precedence file while a lower one
// still says "off" only works if the agent layers them that way, and the one
// thing observation proves is that an override written where the others live
// is honoured.
func OverrideFile(spec config.SkillsSpec, repoRoot, skill string) string {
	if spec.OverridesKey == "" {
		return ""
	}
	fallback := ""
	for _, entry := range spec.SettingsFiles {
		path := settingsPath(entry, repoRoot)
		if path == "" {
			continue
		}
		if fallback == "" {
			fallback = path
		}
		data, err := os.ReadFile(path) // #nosec G304 -- a settings path from the agent registry
		if err != nil {
			continue
		}
		var doc map[string]map[string]State
		if err := json.Unmarshal(data, &doc); err != nil {
			continue
		}
		if _, ok := doc[spec.OverridesKey][skill]; ok {
			return path
		}
	}
	return fallback
}

// SetState switches a skill off for one agent, or clears what switched it off,
// by whichever mechanism that agent has. Callers should not have to know which
// shape a given agent keeps its answer in.
func SetState(spec config.SkillsSpec, repoRoot, skill string, state State) (string, error) {
	switch {
	case spec.OverridesKey != "":
		return SetOverride(spec, repoRoot, skill, state)
	case spec.DisabledKey != "":
		return SetDisabled(spec, repoRoot, skill, state)
	}
	return "", fmt.Errorf("opentree cannot switch a skill off for this agent")
}

// SetDisabled switches a skill off for an agent that keeps a list of the
// disabled rather than a map of states, or takes it off that list again.
//
// A list carries less than a map: a name is on it or it is not, so off and
// default are the only two things it can say. Anything else is refused rather
// than approximated into the nearest thing the list can hold.
//
// Turning a skill back on clears it from every file that names it, not only
// from the one an addition would go to. The files union when read — a name
// missing from one says nothing about the others — so clearing the
// repository's while the user's still lists it would report a skill as on that
// the agent goes on ignoring.
func SetDisabled(spec config.SkillsSpec, repoRoot, skill string, state State) (string, error) {
	if spec.DisabledKey == "" {
		return "", fmt.Errorf("opentree cannot switch a skill off for this agent")
	}
	if state != "" && state != StateOff {
		return "", fmt.Errorf("this agent keeps a list of the disabled, which cannot say %q", state)
	}
	if state == StateOff {
		return setDisabledIn(disabledFile(spec, repoRoot, skill), spec.DisabledKey, skill, true)
	}

	var written string
	var errs []error
	for _, entry := range spec.SettingsFiles {
		path := settingsPath(entry, repoRoot)
		if path == "" || !slices.Contains(disabledIn(path, spec.DisabledKey), skill) {
			continue
		}
		switch p, err := setDisabledIn(path, spec.DisabledKey, skill, false); {
		case err != nil:
			errs = append(errs, err)
		case written == "":
			written = p
		}
	}
	return written, errors.Join(errs...)
}

// disabledFile is where a name should be added: the file already listing it,
// and otherwise the first the agent reads — the same rule OverrideFile
// follows, and for the same reason.
func disabledFile(spec config.SkillsSpec, repoRoot, skill string) string {
	fallback := ""
	for _, entry := range spec.SettingsFiles {
		path := settingsPath(entry, repoRoot)
		if path == "" {
			continue
		}
		if fallback == "" {
			fallback = path
		}
		if slices.Contains(disabledIn(path, spec.DisabledKey), skill) {
			return path
		}
	}
	return fallback
}

// disabledIn is one file's list of the disabled, empty when the file is absent
// or keeps none.
func disabledIn(path, key string) []string {
	data, err := os.ReadFile(path) // #nosec G304 -- a settings path from the agent registry
	if err != nil {
		return nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	return disabledNames(doc, key)
}

// setDisabledIn adds or removes one name in one file's list.
func setDisabledIn(path, key, skill string, add bool) (string, error) {
	if path == "" {
		return "", fmt.Errorf("no settings file to write")
	}
	data, err := os.ReadFile(path) // #nosec G304 -- a settings path from the agent registry
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		data = []byte("{}")
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("%s is not readable as JSON: %w", path, err)
	}

	names := disabledNames(doc, key)
	switch i := slices.Index(names, skill); {
	case add && i < 0:
		names = append(names, skill)
	case !add && i >= 0:
		names = slices.Delete(names, i, i+1)
	default:
		return path, nil // already saying what it was asked to say
	}
	// Never null: an agent reading its own settings back should find a list
	// where it left one, even after the last name comes off.
	if names == nil {
		names = []string{}
	}

	// Two-space indent, as many levels in as the path is deep, to match the
	// document around it. A list nested under "skills" is not at the margin,
	// and marshalling it as if it were leaves the file looking edited.
	keyPath := strings.Split(key, ".")
	value, err := json.MarshalIndent(names, strings.Repeat("  ", len(keyPath)), "  ")
	if err != nil {
		return "", err
	}
	updated, err := replacePath(data, keyPath, value)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, updated); err != nil {
		return "", err
	}
	return path, nil
}

// SetOverride records a skill's state in the agent's settings, or clears the
// override when state is empty.
//
// Clearing rather than writing "on" is the way back to the default, because
// "on" is not always the default: a skill whose own frontmatter asks not to be
// model-invoked would be quietly promoted to fully automatic by an explicit
// "on", which is not what turning it back on means.
//
// Only the overrides object is rewritten. The rest of the file keeps its bytes,
// including the order of keys around it — a settings file is hand-maintained
// and frequently version-controlled, and reformatting all of it to change one
// entry is not a change the user asked for.
func SetOverride(spec config.SkillsSpec, repoRoot, skill string, state State) (string, error) {
	// Reading a disabled list is not the same as writing one: an agent that
	// keeps one can still be told, just not from here.
	if spec.OverridesKey == "" {
		return "", fmt.Errorf("opentree cannot switch a skill off for this agent")
	}
	path := OverrideFile(spec, repoRoot, skill)
	if path == "" {
		return "", fmt.Errorf("no settings file to write")
	}

	data, err := os.ReadFile(path) // #nosec G304 -- a settings path from the agent registry
	if err != nil {
		if !os.IsNotExist(err) {
			return "", err
		}
		data = []byte("{}")
	}

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", fmt.Errorf("%s is not readable as JSON: %w", path, err)
	}

	overrides := map[string]State{}
	if raw, ok := doc[spec.OverridesKey]; ok {
		if err := json.Unmarshal(raw, &overrides); err != nil {
			return "", fmt.Errorf("%s has an unreadable %s: %w", path, spec.OverridesKey, err)
		}
	}
	if state == "" {
		delete(overrides, skill)
	} else {
		overrides[skill] = state
	}

	// Two-space indent, one level in, to match the document around it.
	value, err := json.MarshalIndent(overrides, "  ", "  ")
	if err != nil {
		return "", err
	}
	updated, err := replaceKey(data, spec.OverridesKey, value)
	if err != nil {
		return "", err
	}
	if err := writeFileAtomic(path, updated); err != nil {
		return "", err
	}
	return path, nil
}

// replacePath swaps the value at a dotted path, leaving every other byte of
// the document alone — including the members either side of it at every level.
//
// A missing leaf is inserted into the deepest object that does exist. A branch
// that does not exist at all is written whole, which is the only case where
// opentree chooses the shape of anything it did not find.
func replacePath(data []byte, path []string, value []byte) ([]byte, error) {
	if len(path) == 1 {
		return replaceKey(data, path[0], value)
	}
	start, end, found, err := valueSpan(data, path[0])
	if err != nil {
		return nil, err
	}
	if !found {
		for i := len(path) - 1; i > 0; i-- {
			indent := strings.Repeat("  ", i)
			value = fmt.Appendf(nil, "{\n%s  %q: %s\n%s}", indent, path[i], value, indent)
		}
		return replaceKey(data, path[0], value)
	}
	// Recursing on the sub-object's own bytes, then splicing it back: the
	// offsets a nested span reports are its own, and the outer start is what
	// puts them back where they came from.
	inner, err := replacePath(data[start:end], path[1:], value)
	if err != nil {
		return nil, err
	}
	out := append([]byte{}, data[:start]...)
	out = append(out, inner...)
	return append(out, data[end:]...), nil
}

// replaceKey swaps one top-level key's value in a JSON object, leaving every
// other byte of the document alone. The key is inserted when it is absent.
func replaceKey(data []byte, key string, value []byte) ([]byte, error) {
	start, end, found, err := valueSpan(data, key)
	if err != nil {
		return nil, err
	}
	if found {
		out := make([]byte, 0, len(data)-(end-start)+len(value))
		out = append(out, data[:start]...)
		out = append(out, value...)
		return append(out, data[end:]...), nil
	}

	// Absent: insert as the first member, which needs no knowledge of what
	// separates the existing ones.
	open := bytes.IndexByte(data, '{')
	if open < 0 {
		return nil, fmt.Errorf("not a JSON object")
	}
	entry := fmt.Sprintf("\n  %q: %s", key, value)
	rest := bytes.TrimLeft(data[open+1:], " \t\r\n")
	if len(rest) > 0 && rest[0] != '}' {
		entry += ","
	}
	out := append([]byte{}, data[:open+1]...)
	out = append(out, entry...)
	return append(out, data[open+1:]...), nil
}

// valueSpan is the byte range of a top-level key's value.
func valueSpan(data []byte, key string) (start, end int, found bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return 0, 0, false, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return 0, 0, false, fmt.Errorf("not a JSON object")
	}
	for dec.More() {
		name, err := dec.Token()
		if err != nil {
			return 0, 0, false, err
		}
		afterKey := int(dec.InputOffset())
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return 0, 0, false, err
		}
		if s, _ := name.(string); s != key {
			continue
		}
		// The value begins past the colon and any space after it.
		i := afterKey
		for i < len(data) && data[i] != ':' {
			i++
		}
		for i++; i < len(data) && isJSONSpace(data[i]); i++ {
		}
		return i, int(dec.InputOffset()), true, nil
	}
	return 0, 0, false, nil
}

func isJSONSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// writeFileAtomic writes via a temp file and a rename, so an interrupted write
// cannot leave the user with a truncated settings file.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmpPath, 0600)
	}
	if err == nil {
		err = os.Rename(tmpPath, path)
	}
	if err != nil {
		os.Remove(tmpPath)
		return err
	}
	return nil
}
