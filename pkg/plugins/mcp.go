package plugins

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strings"
)

// MCPSchema is the canonical identifier for the MCP configuration format of
// the Agent Plugins version this build understands. It shares the
// specification's version with ManifestSchema by design, so accepting exactly
// this value is also what enforces the spec's rule that mcp.json and
// plugin.json must target the same version.
const MCPSchema = "https://agent-plugins.org/schemas/1.0.0/mcp.schema.json"

// masked replaces every configured env and header value before it leaves the
// loader. The spec forbids plugins keeping secrets in either, but a rule about
// what should not be in a file is no reason to print what is — and nothing in
// opentree needs the values, because nothing here connects to or launches a
// server. That is issue #36's ground, behind the trust gate.
const masked = "•••"

// Server is one MCP server a plugin declares, as much of it as a listing
// needs. opentree neither launches nor connects to these in v1; the struct
// exists so `plugins list` can say what an install would be agreeing to once
// something does.
type Server struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	Command string            `json:"command,omitempty"` // stdio
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`     // values masked
	URL     string            `json:"url,omitempty"`     // streamable-http and sse
	Headers map[string]string `json:"headers,omitempty"` // values masked
}

// parseMCP validates mcp.json under the spec's failure boundaries: a broken
// top level disables MCP for the plugin and says so, while a broken server
// entry costs only itself. Skills never depend on any of this — a plugin whose
// server configuration is wrong still has every readable skill loaded, because
// the components validate independently.
func parseMCP(data []byte) ([]Server, []string) {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, []string{fmt.Sprintf("mcp.json is not a JSON object: %v — MCP disabled", err)}
	}
	for key := range raw {
		if key != "$schema" && key != "mcpServers" {
			return nil, []string{fmt.Sprintf("mcp.json: unexpected field %q — MCP disabled", key)}
		}
	}

	var schema string
	if err := json.Unmarshal(raw["$schema"], &schema); err != nil || schema == "" {
		return nil, []string{"mcp.json: missing required $schema — MCP disabled"}
	}
	if schema != MCPSchema {
		return nil, []string{fmt.Sprintf("mcp.json: unsupported Agent Plugins version %q — MCP disabled", schema)}
	}
	entries := map[string]json.RawMessage{}
	if msg, ok := raw["mcpServers"]; !ok {
		return nil, []string{"mcp.json: missing required mcpServers — MCP disabled"}
	} else if err := json.Unmarshal(msg, &entries); err != nil {
		return nil, []string{"mcp.json: mcpServers must be an object — MCP disabled"}
	}

	// Sorted so two loads of one file list the same way; JSON object order is
	// the parser's, not the author's.
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)

	var servers []Server
	var problems []string
	for _, name := range names {
		server, err := parseServer(name, entries[name])
		if err != nil {
			problems = append(problems, fmt.Sprintf("mcp server %q skipped: %v", name, err))
			continue
		}
		servers = append(servers, server)
	}
	return servers, problems
}

// parseServer validates one entry against the closed union of transports. The
// union is closed on both sides — an unknown field is as invalid as an unknown
// type — so a typo cannot silently become a server that behaves differently on
// the client that eventually launches it.
func parseServer(name string, msg json.RawMessage) (Server, error) {
	raw := map[string]json.RawMessage{}
	if err := json.Unmarshal(msg, &raw); err != nil {
		return Server{}, fmt.Errorf("not an object")
	}
	var kind string
	if err := json.Unmarshal(raw["type"], &kind); err != nil {
		return Server{}, fmt.Errorf("missing required type")
	}
	switch kind {
	case "stdio":
		return parseStdio(name, raw)
	case "streamable-http", "sse":
		return parseRemote(name, kind, raw)
	}
	return Server{}, fmt.Errorf("unknown type %q", kind)
}

// parseStdio checks the fields that make a local server entry meaningful
// without ever resolving or running the command: the command must be a single
// bare token or a plugin-relative path, cwd one of the three anchored forms,
// and env may not claim the two variables the client itself must supply.
// Containment of a ./-relative command is the caller's to check — only the
// caller knows the plugin root.
func parseStdio(name string, raw map[string]json.RawMessage) (Server, error) {
	s := Server{Name: name, Type: "stdio"}
	for key := range raw {
		switch key {
		case "type", "command", "args", "env", "cwd":
		default:
			return s, fmt.Errorf("unknown field %q", key)
		}
	}
	if err := json.Unmarshal(raw["command"], &s.Command); err != nil || s.Command == "" {
		return s, fmt.Errorf("missing required command")
	}
	if strings.ContainsAny(s.Command, "/\\") && !strings.HasPrefix(s.Command, "./") {
		return s, fmt.Errorf("command must be a bare name or a ./ path")
	}
	if msg, ok := raw["args"]; ok {
		if err := json.Unmarshal(msg, &s.Args); err != nil {
			return s, fmt.Errorf("args must be an array of strings")
		}
	}
	if msg, ok := raw["env"]; ok {
		env := map[string]string{}
		if err := json.Unmarshal(msg, &env); err != nil {
			return s, fmt.Errorf("env must be an object of strings")
		}
		s.Env = map[string]string{}
		for key := range env {
			if key == "PLUGIN_ROOT" || key == "PLUGIN_DATA" {
				return s, fmt.Errorf("env may not set %s", key)
			}
			s.Env[key] = masked
		}
	}
	if msg, ok := raw["cwd"]; ok {
		var cwd string
		if err := json.Unmarshal(msg, &cwd); err != nil {
			return s, fmt.Errorf("cwd must be a string")
		}
		if !anchoredCwd(cwd) {
			return s, fmt.Errorf("cwd must be a ./ path or rooted in ${PLUGIN_ROOT} or ${PLUGIN_DATA}")
		}
	}
	return s, nil
}

// anchoredCwd is the three forms the spec permits, and nothing looser: a bare
// relative path has no defined anchor, and an absolute one points at the
// installing machine rather than the plugin.
func anchoredCwd(cwd string) bool {
	if strings.HasPrefix(cwd, "./") {
		return true
	}
	for _, root := range []string{"${PLUGIN_ROOT}", "${PLUGIN_DATA}"} {
		if cwd == root || strings.HasPrefix(cwd, root+"/") {
			return true
		}
	}
	return false
}

// parseRemote checks a streamable-http or legacy sse entry. The URL rules are
// the spec's security floor — https off the loopback, no credentials in the
// URL — and they are enforced at listing time because an entry that fails them
// should read as invalid in the same place it would later fail to connect.
func parseRemote(name, kind string, raw map[string]json.RawMessage) (Server, error) {
	s := Server{Name: name, Type: kind}
	for key := range raw {
		switch key {
		case "type", "url", "headers":
		default:
			return s, fmt.Errorf("unknown field %q", key)
		}
	}
	if err := json.Unmarshal(raw["url"], &s.URL); err != nil || s.URL == "" {
		return s, fmt.Errorf("missing required url")
	}
	u, err := url.Parse(s.URL)
	if err != nil || !u.IsAbs() {
		return s, fmt.Errorf("url must be an absolute http or https URL")
	}
	if u.User != nil || u.Fragment != "" {
		return s, fmt.Errorf("url may not carry user information or a fragment")
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !loopback(u.Hostname()) {
			return s, fmt.Errorf("http is only allowed on the loopback")
		}
	default:
		return s, fmt.Errorf("url must be an absolute http or https URL")
	}
	if msg, ok := raw["headers"]; ok {
		headers := map[string]string{}
		if err := json.Unmarshal(msg, &headers); err != nil {
			return s, fmt.Errorf("headers must be an object of strings")
		}
		seen := map[string]bool{}
		s.Headers = map[string]string{}
		for key := range headers {
			folded := strings.ToLower(key)
			if seen[folded] {
				return s, fmt.Errorf("header %q appears more than once", folded)
			}
			seen[folded] = true
			s.Headers[key] = masked
		}
	}
	return s, nil
}

// loopback is where the spec lets plain http stand: the literal name
// localhost, or an IP the platform itself keeps on this machine.
func loopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
