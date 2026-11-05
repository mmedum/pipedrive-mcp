package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// sdkVersion is the go-sdk version the dump's header names, so a person
// comparing two dumps by hand can tell an SDK bump from a change to the
// surface. The schema-ack gate leaves the header out.
//
// It mirrors the go.mod pin by hand, so an SDK upgrade can leave it
// behind. TestSDKVersionMatchesGoMod is the check;
// runtime/debug.ReadBuildInfo would remove the constant entirely but does
// not list the dependency in a test binary, so there would be nothing to
// test it with.
const sdkVersion = "v1.8.0"

// dumpHeader is the dump's build stamp: which binary and SDK wrote it,
// and how many tools it lists. The schema gate leaves it out of the
// comparison, since it changes with every build.
type dumpHeader struct {
	BinaryVersion string `json:"binary_version"`
	SDKVersion    string `json:"sdk_version"`
	ToolCount     int    `json:"tool_count"`
}

// dumpDocument is what --dump-schemas writes. Every list is written, an
// empty one as [], so the schema gate can tell a list the server has
// nothing in, such as its resources and prompts, from a list an older
// dump did not carry.
type dumpDocument struct {
	Header            dumpHeader              `json:"header"`
	Instructions      string                  `json:"instructions"`
	Tools             []*mcp.Tool             `json:"tools"`
	ResourceTemplates []*mcp.ResourceTemplate `json:"resource_templates"`
	Resources         []*mcp.Resource         `json:"resources"`
	Prompts           []*mcp.Prompt           `json:"prompts"`
}

// DumpSchemas writes the surface a client is served, as deterministic
// JSON: the instructions it is given at initialize, and the tools,
// resource templates, resources and prompts it lists, which the SDK
// serves sorted by what names each. They are read over an in-memory
// session with the server NewForSchemaDump builds, so the schema gate
// compares the wire rather than a record kept beside it.
func DumpSchemas(w io.Writer, binaryVersion string) error {
	return dumpServer(w, NewForSchemaDump(), binaryVersion)
}

func dumpServer(w io.Writer, s *mcp.Server, binaryVersion string) error {
	ctx := context.Background()
	ct, st := mcp.NewInMemoryTransports()
	ss, err := s.Connect(ctx, st, nil)
	if err != nil {
		return fmt.Errorf("dump schemas: connect server: %w", err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "schema-dump"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		return fmt.Errorf("dump schemas: connect client: %w", err)
	}
	defer func() { _ = cs.Close() }()

	tl, err := cs.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("dump schemas: list tools: %w", err)
	}
	templates, err := cs.ListResourceTemplates(ctx, nil)
	if err != nil {
		return fmt.Errorf("dump schemas: list resource templates: %w", err)
	}
	resources, err := cs.ListResources(ctx, nil)
	if err != nil {
		return fmt.Errorf("dump schemas: list resources: %w", err)
	}
	prompts, err := cs.ListPrompts(ctx, nil)
	if err != nil {
		return fmt.Errorf("dump schemas: list prompts: %w", err)
	}
	doc := dumpDocument{
		Header:            dumpHeader{BinaryVersion: binaryVersion, SDKVersion: sdkVersion, ToolCount: len(tl.Tools)},
		Instructions:      cs.InitializeResult().Instructions,
		Tools:             tl.Tools,
		ResourceTemplates: templates.ResourceTemplates,
		Resources:         resources.Resources,
		Prompts:           prompts.Prompts,
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("dump schemas: %w", err)
	}
	return nil
}
