package tools

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegistry_DumpEmpty(t *testing.T) {
	r := New()

	var buf bytes.Buffer
	if err := r.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}

	var doc dumpDocument
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Header.BinaryVersion != "test" {
		t.Errorf("binary_version = %q, want test", doc.Header.BinaryVersion)
	}
	if doc.Header.SDKVersion != SDKVersion {
		t.Errorf("sdk_version = %q, want %s", doc.Header.SDKVersion, SDKVersion)
	}
	if doc.Header.ToolCount != 0 {
		t.Errorf("tool_count = %d, want 0", doc.Header.ToolCount)
	}
}

func TestRegistry_DumpSortsAlphabetically(t *testing.T) {
	r := New()
	r.Add(&mcp.Tool{Name: "search_deals"})
	r.Add(&mcp.Tool{Name: "create_deal"})
	r.Add(&mcp.Tool{Name: "get_deal"})

	var buf bytes.Buffer
	if err := r.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}

	out := buf.String()
	createIdx := strings.Index(out, `"create_deal"`)
	getIdx := strings.Index(out, `"get_deal"`)
	searchIdx := strings.Index(out, `"search_deals"`)
	if createIdx < 0 || getIdx < 0 || searchIdx < 0 {
		t.Fatalf("missing tool names in output: %s", out)
	}
	if createIdx >= getIdx || getIdx >= searchIdx {
		t.Errorf("tools not sorted alphabetically: create=%d get=%d search=%d",
			createIdx, getIdx, searchIdx)
	}
}

func TestRegistry_AddIsConcurrent(t *testing.T) {
	r := New()

	const n = 64
	done := make(chan struct{})
	for i := 0; i < n; i++ {
		go func(i int) {
			r.Add(&mcp.Tool{Name: strconv.Itoa(i)})
			done <- struct{}{}
		}(i)
	}
	for i := 0; i < n; i++ {
		<-done
	}

	var buf bytes.Buffer
	if err := r.DumpJSON(&buf, "test"); err != nil {
		t.Fatalf("DumpJSON: %v", err)
	}
	var doc dumpDocument
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if doc.Header.ToolCount != n {
		t.Errorf("tool_count = %d, want %d", doc.Header.ToolCount, n)
	}
}
