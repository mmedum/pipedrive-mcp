package server

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// named is a dumped item read by its name.
type named struct {
	Name string `json:"name"`
}

// dumped is the parts of a dump the tests read.
type dumped struct {
	Header struct {
		BinaryVersion string `json:"binary_version"`
		SDKVersion    string `json:"sdk_version"`
		ToolCount     int    `json:"tool_count"`
	} `json:"header"`
	Instructions      string  `json:"instructions"`
	Tools             []named `json:"tools"`
	ResourceTemplates []struct {
		URITemplate string `json:"uriTemplate"`
	} `json:"resource_templates"`
	Resources []struct {
		URI string `json:"uri"`
	} `json:"resources"`
	Prompts []named `json:"prompts"`
}

func dumpOf(t *testing.T, write func(*bytes.Buffer) error) (dumped, map[string]any) {
	t.Helper()
	var buf bytes.Buffer
	if err := write(&buf); err != nil {
		t.Fatal(err)
	}
	var d dumped
	var keys map[string]any
	if err := json.Unmarshal(buf.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(buf.Bytes(), &keys); err != nil {
		t.Fatal(err)
	}
	return d, keys
}

// The dump is what a client is served: the instructions it is given and
// the resource templates it lists as well as the tools, each list sorted,
// under a header that names the build.
func TestTheDumpIsWhatAClientIsServed(t *testing.T) {
	d, keys := dumpOf(t, func(b *bytes.Buffer) error { return DumpSchemas(b, "v9.9.9-test") })
	if d.Header.BinaryVersion != "v9.9.9-test" || d.Header.SDKVersion != sdkVersion {
		t.Errorf("header = %+v, want the binary version passed in and SDK %s", d.Header, sdkVersion)
	}
	if d.Header.ToolCount == 0 || d.Header.ToolCount != len(d.Tools) {
		t.Errorf("tool_count = %d for %d tools", d.Header.ToolCount, len(d.Tools))
	}
	if !slices.IsSortedFunc(d.Tools, func(a, b named) int { return strings.Compare(a.Name, b.Name) }) {
		t.Errorf("the tools are not sorted by name: %v", d.Tools)
	}
	if want := "Pipedrive CRM tools, against one workspace's API token."; !strings.HasPrefix(d.Instructions, want) {
		t.Errorf("instructions = %.80q..., want the server's, which start %q", d.Instructions, want)
	}
	templates := make([]string, 0, len(d.ResourceTemplates))
	for _, r := range d.ResourceTemplates {
		templates = append(templates, r.URITemplate)
	}
	want := []string{"pipedrive://activities/{id}", "pipedrive://deals/{id}", "pipedrive://notes/{id}",
		"pipedrive://organizations/{id}", "pipedrive://persons/{id}"}
	if !slices.Equal(templates, want) {
		t.Errorf("resource templates = %v, want %v", templates, want)
	}
	for _, k := range []string{"resources", "prompts"} {
		if list, ok := keys[k].([]any); !ok || len(list) != 0 {
			t.Errorf("%s = %#v, want [], an empty list, which the server has", k, keys[k])
		}
	}
}

// The server has no resources or prompts today. One added later is in
// the dump, sorted, so the schema gate sees it.
func TestTheDumpListsResourcesAndPrompts(t *testing.T) {
	s := mcp.NewServer(&mcp.Implementation{Name: "fixture"}, nil)
	read := func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{}, nil
	}
	get := func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{}, nil
	}
	s.AddResource(&mcp.Resource{URI: "pipedrive://b", Name: "b"}, read)
	s.AddResource(&mcp.Resource{URI: "pipedrive://a", Name: "a"}, read)
	s.AddPrompt(&mcp.Prompt{Name: "review"}, get)
	s.AddPrompt(&mcp.Prompt{Name: "plan"}, get)
	d, _ := dumpOf(t, func(b *bytes.Buffer) error { return dumpServer(b, s, "test") })
	got := make([]string, 0, len(d.Resources)+len(d.Prompts))
	for _, r := range d.Resources {
		got = append(got, r.URI)
	}
	for _, p := range d.Prompts {
		got = append(got, p.Name)
	}
	if want := []string{"pipedrive://a", "pipedrive://b", "plan", "review"}; !slices.Equal(got, want) {
		t.Errorf("the dump lists %v, want %v", got, want)
	}
}

// sdkVersion is written into the dump's header, where its whole job is
// to let a reviewer tell "the SDK moved" from "the surface changed". A
// stale value says the opposite of what it is for, and it has been stale
// twice.
func TestSDKVersionMatchesGoMod(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	const mod = "github.com/modelcontextprotocol/go-sdk "
	var pinned string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, mod); ok {
			pinned = strings.Fields(after)[0]
			break
		}
	}
	if pinned == "" {
		t.Fatalf("go.mod has no %s requirement; this test is asserting on nothing", mod)
	}
	if sdkVersion != pinned {
		t.Errorf("sdkVersion is %q and go.mod pins %q; the dump's header would "+
			"attribute a diff to the wrong SDK version", sdkVersion, pinned)
	}
}
