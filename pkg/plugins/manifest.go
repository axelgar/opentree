package plugins

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ManifestSchema is the canonical identifier for the one Agent Plugins version
// this build understands. The spec forbids fetching a schema while loading, so
// the identifier works as a version declaration rather than a URL: anything
// else is a version this binary has no rules for, and guessing at forward
// compatibility would mean executing a contract it never read.
const ManifestSchema = "https://agent-plugins.org/schemas/1.0.0/plugin.schema.json"

// manifest is plugin.json once it has passed validation. Only the fields the
// listing shows are kept; the rest are validated for type and dropped, because
// a closed schema is a promise about what may appear, not an obligation to
// remember all of it.
type manifest struct {
	Name        string
	Version     string
	Description string
}

// manifestKeys is the closed schema: the exact top-level fields the spec
// permits, and the shape each must have. Anything else in the file is an
// unknown field, which the spec makes non-fatal — report it, ignore it, and
// keep loading — so that a plugin written against a later minor version still
// installs on a client that predates it.
var manifestKeys = map[string]bool{
	"$schema": true, "name": true, "version": true, "description": true,
	"author": true, "homepage": true, "repository": true, "license": true,
	"keywords": true, "extensions": true,
}

// parseManifest validates plugin.json and returns it with every non-fatal
// deviation reported. An error here rejects the whole plugin: the spec draws
// exactly two non-fatal exceptions — an unknown top-level field and a
// non-object extensions value — and makes every other violation fatal, so a
// package that cannot state its own name or version contract never gets any
// of its components loaded.
func parseManifest(data []byte) (manifest, []string, error) {
	var m manifest
	var problems []string

	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return m, nil, fmt.Errorf("plugin.json is not a JSON object: %w", err)
	}

	for key := range raw {
		if !manifestKeys[key] {
			problems = append(problems, fmt.Sprintf("plugin.json: unknown field %q ignored", key))
		}
	}

	schema, err := stringField(raw, "$schema")
	if err != nil {
		return m, nil, err
	}
	if schema == "" {
		return m, nil, fmt.Errorf("plugin.json: missing required $schema")
	}
	if schema != ManifestSchema {
		return m, nil, fmt.Errorf("plugin.json: unsupported Agent Plugins version %q (this opentree speaks %s)", schema, ManifestSchema)
	}

	if m.Name, err = stringField(raw, "name"); err != nil {
		return m, nil, err
	}
	if err := checkName(m.Name); err != nil {
		return m, nil, fmt.Errorf("plugin.json: %w", err)
	}

	// The remaining metadata validates by JSON type only. The spec is explicit
	// that a version that is not semver or a homepage that is not a URL must
	// not reject the manifest — their content is advisory, their type is not.
	if m.Version, err = stringField(raw, "version"); err != nil {
		return m, nil, err
	}
	if m.Description, err = stringField(raw, "description"); err != nil {
		return m, nil, err
	}
	for _, key := range []string{"homepage", "repository", "license"} {
		if _, err := stringField(raw, key); err != nil {
			return m, nil, err
		}
	}
	if msg, ok := raw["keywords"]; ok {
		var keywords []string
		if err := json.Unmarshal(msg, &keywords); err != nil {
			return m, nil, fmt.Errorf("plugin.json: keywords must be an array of strings")
		}
	}
	if msg, ok := raw["author"]; ok {
		if err := checkAuthor(msg); err != nil {
			return m, nil, err
		}
	}
	if msg, ok := raw["extensions"]; ok {
		// The one other non-fatal shape error. Members of a valid extensions
		// object are namespaces opentree does not implement, and the spec
		// forbids validating the contents of namespaces a client ignores.
		var ext map[string]json.RawMessage
		if err := json.Unmarshal(msg, &ext); err != nil {
			problems = append(problems, "plugin.json: extensions is not an object, ignored")
		}
	}

	return m, problems, nil
}

// stringField reads an optional top-level string, failing on any other type —
// the closed schema means a mistyped field is a violation, not a curiosity.
func stringField(raw map[string]json.RawMessage, key string) (string, error) {
	msg, ok := raw[key]
	if !ok {
		return "", nil
	}
	var s string
	if err := json.Unmarshal(msg, &s); err != nil {
		return "", fmt.Errorf("plugin.json: %s must be a string", key)
	}
	return s, nil
}

// checkAuthor enforces the one nested shape the manifest schema closes: an
// object holding at most name, email and url, each a string. Any other member
// or type invalidates the manifest as a whole.
func checkAuthor(msg json.RawMessage) error {
	var author map[string]json.RawMessage
	if err := json.Unmarshal(msg, &author); err != nil {
		return fmt.Errorf("plugin.json: author must be an object")
	}
	for key, value := range author {
		if key != "name" && key != "email" && key != "url" {
			return fmt.Errorf("plugin.json: author has no field %q", key)
		}
		var s string
		if err := json.Unmarshal(value, &s); err != nil {
			return fmt.Errorf("plugin.json: author.%s must be a string", key)
		}
	}
	return nil
}

// checkName enforces the spec's naming constraints. The name doubles as the
// store directory here, so the constraints are also what make it safe to use
// as a path element without any escaping of opentree's own.
func checkName(name string) error {
	if name == "" {
		return fmt.Errorf("missing required name")
	}
	if len(name) > 64 {
		return fmt.Errorf("name %q is longer than 64 characters", name)
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' && r != '.' {
			return fmt.Errorf("name %q may hold only a-z, 0-9, '-' and '.'", name)
		}
	}
	if !alnum(name[0]) || !alnum(name[len(name)-1]) {
		return fmt.Errorf("name %q must start and end with a letter or digit", name)
	}
	if strings.Contains(name, "--") || strings.Contains(name, "..") {
		return fmt.Errorf("name %q may not repeat '-' or '.'", name)
	}
	return nil
}

func alnum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}
