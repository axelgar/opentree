package skills

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/axelgar/opentree/pkg/config"
)

// The built-in directories are where skills usually live, not where they have
// to. An agent lets its user register more, and opentree reading only the
// built-in ones fails that user silently — a short list looks exactly like a
// machine with few skills.
//
// Two agents have this today, under keys of their own: "skills" in
// opencode.json, "skillDirectories" in Copilot's settings. Both search what is
// named there recursively rather than one level deep — observed of Copilot,
// documented by opencode — which is the reason Tree carries Deep.

// configTrees are the extra skills directories an agent's own config declares.
func configTrees(spec config.SkillsSpec, repoRoot string) []string {
	if spec.ConfigKey == "" {
		return nil
	}
	var out []string
	for _, entry := range spec.ConfigFiles {
		path := settingsPath(entry, repoRoot)
		if path == "" {
			continue
		}
		for _, dir := range declaredDirs(path, spec.ConfigKey) {
			if !slices.Contains(out, dir) {
				out = append(out, dir)
			}
		}
	}
	return out
}

// declaredDirs reads one config document's registered skills directories.
//
// Two shapes are accepted because the agent itself accepts both and migrates
// between them: a flat array of entries, and the older object of "paths" and
// "urls". A URL is skipped rather than reported — it serves a remote list
// rather than naming a directory here, and none of the things this tab offers
// would work on one.
//
// Relative entries resolve against the config file's own directory, which is
// what makes ./skills mean the same thing to opentree as it does to the agent
// reading it.
func declaredDirs(path, key string) []string {
	data, err := os.ReadFile(path) // #nosec G304 -- a config path from the agent registry
	if err != nil {
		return nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(stripJSONC(data), &doc); err != nil || len(doc[key]) == 0 {
		return nil
	}

	var entries []string
	if err := json.Unmarshal(doc[key], &entries); err != nil {
		var older struct {
			Paths []string `json:"paths"`
		}
		if err := json.Unmarshal(doc[key], &older); err != nil {
			return nil
		}
		entries = older.Paths
	}

	base := filepath.Dir(path)
	var out []string
	for _, entry := range entries {
		if entry == "" || strings.Contains(entry, "://") {
			continue
		}
		dir := ExpandUserDir(entry)
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(base, dir)
		}
		out = append(out, dir)
	}
	return out
}

// stripJSONC removes the comments and trailing commas a .jsonc config may
// carry, so encoding/json can read it.
//
// Needed rather than tolerated: the alternative to reading a commented config
// is reporting that it registers no skills, which is the failure this whole
// file exists to avoid. Whitespace outside strings goes too — it means nothing
// to the parser, and dropping it is what lets a trailing comma be recognised
// by the character that follows it.
func stripJSONC(data []byte) []byte {
	out := make([]byte, 0, len(data))
	var inString, escaped, pendingComma bool

	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}

		switch {
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			i += 2
			for i+1 < len(data) && (data[i] != '*' || data[i+1] != '/') {
				i++
			}
			i++
		case c == ',':
			// Held rather than written: whether it is a separator or a trailing
			// comma is decided by the next character that means anything.
			pendingComma = true
		case c <= ' ':
			// insignificant outside a string
		default:
			if pendingComma {
				if c != '}' && c != ']' {
					out = append(out, ',')
				}
				pendingComma = false
			}
			if c == '"' {
				inString = true
			}
			out = append(out, c)
		}
	}
	return out
}
