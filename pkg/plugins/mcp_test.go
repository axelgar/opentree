package plugins

import (
	"strings"
	"testing"
)

func mcpDoc(servers string) string {
	return `{"$schema": "` + MCPSchema + `", "mcpServers": {` + servers + `}}`
}

// The spec's own example, near enough: one of each transport, loaded in name
// order, with every env and header value masked before it leaves the parser.
func TestParseMCP(t *testing.T) {
	doc := mcpDoc(`
		"local-validator": {"type": "stdio", "command": "./bin/validator",
			"args": ["--data", "${PLUGIN_DATA}/validator"],
			"env": {"CONFIG": "${PLUGIN_ROOT}/config.json"}, "cwd": "${PLUGIN_ROOT}"},
		"deployment-api": {"type": "streamable-http", "url": "https://deploy.example.com/mcp",
			"headers": {"X-Tenant": "public-tenant"}},
		"legacy-events": {"type": "sse", "url": "https://legacy.example.com/sse"}`)

	servers, problems := parseMCP([]byte(doc))
	if len(problems) != 0 {
		t.Fatalf("problems = %v, want none", problems)
	}
	if len(servers) != 3 {
		t.Fatalf("servers = %d, want 3", len(servers))
	}
	if servers[0].Name != "deployment-api" || servers[1].Name != "legacy-events" || servers[2].Name != "local-validator" {
		t.Fatalf("servers are not in name order: %v", servers)
	}
	if got := servers[2].Env["CONFIG"]; got != masked {
		t.Errorf("an env value left the parser unmasked: %q", got)
	}
	if got := servers[0].Headers["X-Tenant"]; got != masked {
		t.Errorf("a header value left the parser unmasked: %q", got)
	}
	if servers[2].Command != "./bin/validator" || servers[0].URL != "https://deploy.example.com/mcp" {
		t.Error("the targets a listing shows were lost")
	}
}

// A broken top level disables the whole component and says so; nothing of the
// entries is worth trusting once the document's own contract failed.
func TestParseMCP_DisablesOnABrokenTopLevel(t *testing.T) {
	tests := []struct {
		name string
		doc  string
	}{
		{"not json", `nope`},
		{"missing $schema", `{"mcpServers": {}}`},
		{"wrong version", `{"$schema": "https://agent-plugins.org/schemas/9.0.0/mcp.schema.json", "mcpServers": {}}`},
		{"missing mcpServers", `{"$schema": "` + MCPSchema + `"}`},
		{"extra field", `{"$schema": "` + MCPSchema + `", "mcpServers": {}, "extra": 1}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			servers, problems := parseMCP([]byte(tt.doc))
			if servers != nil {
				t.Fatalf("a broken document still produced servers: %v", servers)
			}
			if len(problems) != 1 || !strings.Contains(problems[0], "MCP disabled") {
				t.Fatalf("problems = %v, want one saying MCP is disabled", problems)
			}
		})
	}
}

// An empty mcpServers object is explicitly valid: a plugin may declare the
// component and have nothing in it yet.
func TestParseMCP_AcceptsAnEmptyServerSet(t *testing.T) {
	servers, problems := parseMCP([]byte(mcpDoc("")))
	if len(servers) != 0 || len(problems) != 0 {
		t.Fatalf("servers = %v, problems = %v, want neither", servers, problems)
	}
}

// One bad entry costs itself and nothing else — the spec's failure boundary,
// and the reason a typo in one server does not hide a plugin's other two.
func TestParseMCP_SkipsOnlyTheBrokenEntry(t *testing.T) {
	tests := []struct {
		name   string
		entry  string
		reason string
	}{
		{"unknown type", `{"type": "websocket", "url": "https://x.example"}`, "unknown type"},
		{"unknown field", `{"type": "stdio", "command": "x", "cmd": "y"}`, `unknown field "cmd"`},
		{"missing command", `{"type": "stdio"}`, "missing required command"},
		{"shell command string", `{"type": "stdio", "command": "bin/tool"}`, "bare name or a ./ path"},
		{"reserved env", `{"type": "stdio", "command": "x", "env": {"PLUGIN_ROOT": "/tmp"}}`, "may not set PLUGIN_ROOT"},
		{"unanchored cwd", `{"type": "stdio", "command": "x", "cwd": "data"}`, "cwd must be"},
		{"absolute cwd", `{"type": "stdio", "command": "x", "cwd": "/etc"}`, "cwd must be"},
		{"missing url", `{"type": "sse"}`, "missing required url"},
		{"relative url", `{"type": "sse", "url": "/mcp"}`, "absolute http or https"},
		{"credentials in url", `{"type": "sse", "url": "https://user:pw@x.example/mcp"}`, "user information"},
		{"plain http off loopback", `{"type": "streamable-http", "url": "http://x.example/mcp"}`, "loopback"},
		{"duplicate header", `{"type": "sse", "url": "https://x.example", "headers": {"X-A": "1", "x-a": "2"}}`,
			"more than once"},
		{"field of the other variant", `{"type": "sse", "url": "https://x.example", "command": "x"}`,
			`unknown field "command"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := mcpDoc(`"bad": ` + tt.entry + `, "good": {"type": "stdio", "command": "ok"}`)
			servers, problems := parseMCP([]byte(doc))
			if len(servers) != 1 || servers[0].Name != "good" {
				t.Fatalf("servers = %v, want just the good one", servers)
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tt.reason) {
				t.Fatalf("problems = %v, want one naming %q", problems, tt.reason)
			}
		})
	}
}

// http on the loopback is the one plaintext the spec allows — local servers
// have nowhere to leak to.
func TestParseMCP_AllowsPlainHTTPOnTheLoopback(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]"} {
		doc := mcpDoc(`"local": {"type": "streamable-http", "url": "http://` + host + `:3000/mcp"}`)
		servers, problems := parseMCP([]byte(doc))
		if len(servers) != 1 || len(problems) != 0 {
			t.Errorf("host %s: servers = %v, problems = %v", host, servers, problems)
		}
	}
}
