package registry

import (
	"fmt"
	"hash/fnv"

	"github.com/axelgar/opentree/pkg/config"
)

// palette is the colours a registry agent can wear. The predefined four wear
// their own brands' colours; a registry entry has no BrandSpec, and inventing
// a per-agent colour would make the colours stop meaning anything — so the
// choice is deterministic in the id, from a short list picked to stay apart
// from the four brand colours and from each other on a dark or light
// terminal. Same id, same colour, on every machine.
var palette = []string{
	"#56B6C2", // cyan
	"#98C379", // green
	"#E5C07B", // gold
	"#E06C75", // red
	"#61AFEF", // sky
	"#C678DD", // magenta
}

// registryMark is every registry agent's glyph. One shared mark rather than
// a per-agent guess: the mark's job is telling agents apart at a glance, and
// forty entries cannot each have a distinct single glyph — but "this one
// came from the registry" is itself worth a glance. Plain geometry, like the
// others, because emoji render double-width in some terminals.
const registryMark = "◇"

// brandFor is the on-screen identity a registry agent gets: the shared mark,
// a colour hashed from the id, and the small fallback logo the chat would
// draw anyway — spelled out so the "every agent has a logo" invariant holds
// by construction rather than by fallback.
func brandFor(id string) config.BrandSpec {
	h := fnv.New32a()
	_, _ = h.Write([]byte(id))
	return config.BrandSpec{
		Colour: palette[int(h.Sum32())%len(palette)],
		Mark:   registryMark,
		Logo:   []string{"", "  " + registryMark, ""},
	}
}

// synthesize is the Record as a runtime agent. Command is the registry id —
// what config files, workspace state and `--agents` name, portable across
// machines — and the ACP command is the absolute path the install resolved,
// which IsInstalled and the chat already know how to treat. Skills stay
// empty: the entry does not say where the agent reads skills from, and a
// guessed directory produces confident false warnings in the skills tab.
func synthesize(r Record) config.PredefinedAgent {
	return config.PredefinedAgent{
		Name:        r.Entry.Name,
		Command:     r.Entry.ID,
		Description: r.Entry.Description,
		ACP: config.ACPSpec{
			Command: r.Command,
			Args:    r.Args,
			Env:     r.Env,
		},
		Brand:  brandFor(r.Entry.ID),
		Origin: &config.RegistryOrigin{ID: r.Entry.ID, Version: r.Entry.Version, Dir: r.Dir},
	}
}

// LoadInstalled makes the runtime registry reflect the store: the built-in
// agents, then every installed registry agent, in a freshly built slice
// that replaces config.PredefinedAgents. It returns the problems worth
// telling doctor about.
//
// Replace, never append. FindAgent and the picker hand out pointers into
// the slice, and an append could move it out from under them; a
// replacement leaves every pointer already taken pointing at the old
// backing array — valid memory, merely stale — and everyone who asks after
// the reload sees the new list. That makes the call idempotent, which is
// what lets the dashboard reload after an install or removal instead of
// asking for a restart. The one rule for callers: do not hold a pointer
// across a reload; take it, use it, let it go.
//
// A record whose id or name a built-in already answers to is skipped,
// built-ins first: if a future opentree ships an agent the user once
// installed from the registry, the shipped one — pinned, branded, tested —
// wins, and the stale install is reported rather than shadowed silently.
func LoadInstalled() []string {
	builtins := make([]config.PredefinedAgent, 0, len(config.PredefinedAgents))
	for _, a := range config.PredefinedAgents {
		if a.Origin == nil {
			builtins = append(builtins, a)
		}
	}
	config.PredefinedAgents = builtins

	records, problems := Installed()
	for _, r := range records {
		if config.FindAgent(r.Entry.ID) != nil || config.FindAgent(r.Entry.Name) != nil {
			problems = append(problems, fmt.Sprintf(
				"%s: opentree already has an agent by that name — the built-in wins; `opentree agents remove %s` clears the install",
				r.Entry.ID, r.Entry.ID))
			continue
		}
		config.PredefinedAgents = append(config.PredefinedAgents, synthesize(r))
	}
	return problems
}
