package plugins

import (
	"strings"
	"testing"
)

// The spec draws a sharp line through the manifest: exactly two shape errors
// are survivable, everything else rejects the plugin whole. The table walks
// both sides of that line, because a loader that is lenient in the wrong
// place executes a contract it never read.
func TestParseManifest(t *testing.T) {
	minimal := `{"$schema": "` + ManifestSchema + `", "name": "minimal-plugin"}`

	tests := []struct {
		name     string
		json     string
		fatal    string // substring of the rejection, "" when the manifest loads
		problems int
	}{
		{"minimal", minimal, "", 0},
		{"full", `{
			"$schema": "` + ManifestSchema + `",
			"name": "plugin-name", "version": "1.2.0",
			"description": "Brief plugin description",
			"author": {"name": "A", "email": "a@example.com", "url": "https://example.com"},
			"homepage": "https://docs.example.com", "repository": "https://github.com/example/plugin",
			"license": "MIT", "keywords": ["k1", "k2"],
			"extensions": {"com.example.client": {"setting": true}}
		}`, "", 0},
		{"not an object", `[1, 2]`, "not a JSON object", 0},
		{"missing $schema", `{"name": "x"}`, "missing required $schema", 0},
		{"unsupported version", `{"$schema": "https://agent-plugins.org/schemas/2.0.0/plugin.schema.json", "name": "x"}`,
			"unsupported Agent Plugins version", 0},
		{"missing name", `{"$schema": "` + ManifestSchema + `"}`, "missing required name", 0},
		{"name wrong type", `{"$schema": "` + ManifestSchema + `", "name": 5}`, "name must be a string", 0},
		{"version wrong type", `{"$schema": "` + ManifestSchema + `", "name": "x", "version": 1.2}`,
			"version must be a string", 0},
		{"keywords wrong type", `{"$schema": "` + ManifestSchema + `", "name": "x", "keywords": "k"}`,
			"keywords must be an array", 0},
		{"author not an object", `{"$schema": "` + ManifestSchema + `", "name": "x", "author": "someone"}`,
			"author must be an object", 0},
		{"author unknown field", `{"$schema": "` + ManifestSchema + `", "name": "x", "author": {"github": "a"}}`,
			`author has no field "github"`, 0},
		{"author non-string value", `{"$schema": "` + ManifestSchema + `", "name": "x", "author": {"name": 1}}`,
			"author.name must be a string", 0},
		// The two survivable deviations: reported, ignored, loaded anyway.
		{"unknown field", `{"$schema": "` + ManifestSchema + `", "name": "x", "commands": "./cmd"}`, "", 1},
		{"extensions not an object", `{"$schema": "` + ManifestSchema + `", "name": "x", "extensions": "yes"}`, "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, problems, err := parseManifest([]byte(tt.json))
			if tt.fatal != "" {
				if err == nil {
					t.Fatalf("parseManifest accepted %s", tt.name)
				}
				if !strings.Contains(err.Error(), tt.fatal) {
					t.Fatalf("rejection %q does not name the failure %q", err, tt.fatal)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseManifest rejected a valid manifest: %v", err)
			}
			if len(problems) != tt.problems {
				t.Fatalf("problems = %v, want %d of them", problems, tt.problems)
			}
			if m.Name == "" {
				t.Fatal("a loaded manifest lost its name")
			}
		})
	}
}

// Metadata content is advisory: the spec forbids rejecting a manifest because
// a version is not semver or a homepage is not a URL. Only the types bind.
func TestParseManifest_DoesNotJudgeMetadataContent(t *testing.T) {
	doc := `{"$schema": "` + ManifestSchema + `", "name": "x",
		"version": "not semver", "homepage": "not a url", "license": "not spdx"}`
	if _, _, err := parseManifest([]byte(doc)); err != nil {
		t.Fatalf("parseManifest judged metadata content: %v", err)
	}
}

func TestCheckName(t *testing.T) {
	valid := []string{"my-plugin", "acme.tools", "lint3r", "a", strings.Repeat("a", 64)}
	for _, name := range valid {
		if err := checkName(name); err != nil {
			t.Errorf("checkName(%q) = %v, want nil", name, err)
		}
	}
	invalid := []string{"", "My-Plugin", "-start", "end-", ".start", "has--double", "too.many..dots",
		"has space", "sla/sh", strings.Repeat("a", 65)}
	for _, name := range invalid {
		if err := checkName(name); err == nil {
			t.Errorf("checkName(%q) accepted an invalid name", name)
		}
	}
}
