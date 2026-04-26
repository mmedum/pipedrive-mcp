// Package tools holds MCP tool registrations. Each resource type lives in
// its own file (deals.go, persons.go, ...) and exposes a Register
// function that the server's startup wires up.
//
// Every tool is added to *both* the SDK (via mcp.AddTool) and the
// registry below. The registry powers the
// `pipedrive-mcp --dump-schemas` flag, which produces the deterministic
// JSON the schema-diff CI gate compares against the previous release tag.
//
// The MCP Go SDK does not expose a public Server.Tools() to walk
// registered tools, so this parallel registry is the workaround. If a
// future SDK version ships ListTools(), drop this and switch.
package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// SDKVersion is recorded in the schema dump header so a diff caused by an
// SDK upgrade can be classified as PATCH rather than as a breaking
// surface change. Update in lockstep with the go.mod pin.
const SDKVersion = "v1.5.0"

// Registry tracks every tool added via Register/Add for the schema dump.
// Tests construct a fresh Registry; the process-wide registration uses
// Default.
type Registry struct {
	mu    sync.Mutex
	tools []*mcp.Tool
}

// New returns an empty Registry.
func New() *Registry { return &Registry{} }

// Add records a tool. Call after a successful mcp.AddTool.
func (r *Registry) Add(t *mcp.Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools = append(r.tools, t)
}

// DumpJSON writes the registered tools as deterministic JSON. Tools are
// sorted alphabetically by name. The header records the SDK version and
// the binary version (passed in by the caller — usually
// internal/version.Version).
func (r *Registry) DumpJSON(w io.Writer, binaryVersion string) error {
	r.mu.Lock()
	tools := make([]*mcp.Tool, len(r.tools))
	copy(tools, r.tools)
	r.mu.Unlock()

	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Name < tools[j].Name
	})

	doc := dumpDocument{
		Header: dumpHeader{
			BinaryVersion: binaryVersion,
			SDKVersion:    SDKVersion,
			ToolCount:     len(tools),
		},
		Tools: tools,
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("tools: dump schemas: %w", err)
	}
	return nil
}

// Default is the process-wide registry. Phase 1+ tool packages call
// Default.Add after mcp.AddTool.
var Default = New()

// Add records a tool in the process-wide Default registry.
func Add(t *mcp.Tool) { Default.Add(t) }

// DumpJSON writes the process-wide Default registry as JSON.
func DumpJSON(w io.Writer, binaryVersion string) error {
	return Default.DumpJSON(w, binaryVersion)
}

type dumpHeader struct {
	BinaryVersion string `json:"binary_version"`
	SDKVersion    string `json:"sdk_version"`
	ToolCount     int    `json:"tool_count"`
}

type dumpDocument struct {
	Header dumpHeader  `json:"header"`
	Tools  []*mcp.Tool `json:"tools"`
}
