package tools_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mmedum/pipedrive-mcp/internal/pipedrive"
	"github.com/mmedum/pipedrive-mcp/internal/server/testutil"
	"github.com/mmedum/pipedrive-mcp/internal/tools"
)

type fakeResourcesClient struct {
	deal *pipedrive.Deal
	err  error
}

func (f *fakeResourcesClient) GetDeal(context.Context, int64) (*pipedrive.Deal, error) {
	return f.deal, f.err
}
func (f *fakeResourcesClient) GetPerson(context.Context, int64) (*pipedrive.Person, error) {
	return &pipedrive.Person{ID: 3, Name: "A Contact"}, f.err
}
func (f *fakeResourcesClient) GetOrganization(context.Context, int64) (*pipedrive.Organization, error) {
	return &pipedrive.Organization{ID: 47, Name: "Acme Inc"}, f.err
}
func (f *fakeResourcesClient) GetActivity(context.Context, int64, pipedrive.GetActivityOptions) (*pipedrive.Activity, error) {
	return &pipedrive.Activity{ID: 5, Subject: "Call"}, f.err
}
func (f *fakeResourcesClient) GetNote(context.Context, int64) (*pipedrive.Note, error) {
	return &pipedrive.Note{ID: 77, Content: "<p>x</p>", ActiveFlag: true}, f.err
}
func (f *fakeResourcesClient) ResolveDealCustomFields(_ context.Context, raw map[string]any) map[string]any {
	return raw
}
func (f *fakeResourcesClient) ResolvePersonCustomFields(_ context.Context, raw map[string]any) map[string]any {
	return raw
}
func (f *fakeResourcesClient) ResolveOrganizationCustomFields(_ context.Context, raw map[string]any) map[string]any {
	return raw
}

func resourcesHarness(t *testing.T, fake *fakeResourcesClient) *testutil.Harness {
	t.Helper()
	return testutil.Connect(t, func(s *mcp.Server) {
		tools.RegisterResources(s, fake, "acme")
	})
}

func TestResources_TemplatesAreListed(t *testing.T) {
	h := resourcesHarness(t, &fakeResourcesClient{})
	defer h.Close()

	res, err := h.Client.ListResourceTemplates(context.Background(), &mcp.ListResourceTemplatesParams{})
	if err != nil {
		t.Fatalf("ListResourceTemplates: %v", err)
	}
	got := map[string]string{}
	for _, tmpl := range res.ResourceTemplates {
		got[tmpl.Name] = tmpl.URITemplate
	}
	for _, want := range []string{"deals", "persons", "organizations", "activities", "notes"} {
		uri, ok := got[want]
		if !ok {
			t.Errorf("no resource template for %q", want)
			continue
		}
		if !strings.HasPrefix(uri, "pipedrive://"+want+"/") {
			t.Errorf("%s template = %q; want the pipedrive:// scheme", want, uri)
		}
	}
}

func TestResources_ReadMirrorsTheGetTool(t *testing.T) {
	// The resource and the tool must describe the same record the same
	// way; they share the summarize function precisely so they cannot
	// drift apart.
	fake := &fakeResourcesClient{
		deal: &pipedrive.Deal{ID: 9, Title: "Acme renewal", Value: 5000, Currency: "EUR", Status: "open"},
	}
	h := resourcesHarness(t, fake)
	defer h.Close()

	res, err := h.Client.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "pipedrive://deals/9"})
	if err != nil {
		t.Fatalf("ReadResource: %v", err)
	}
	if len(res.Contents) != 1 {
		t.Fatalf("contents = %d; want 1", len(res.Contents))
	}
	if res.Contents[0].MIMEType != "application/json" {
		t.Errorf("mime = %q; want application/json", res.Contents[0].MIMEType)
	}
	var got dealRow
	if err := json.Unmarshal([]byte(res.Contents[0].Text), &got); err != nil {
		t.Fatalf("unmarshal resource body: %v", err)
	}
	if got.ID != 9 || got.Title != "Acme renewal" || got.Currency != "EUR" {
		t.Errorf("deal = %+v; want the same record get_deal returns", got)
	}
	// The URL injection the tool does must survive into the resource.
	if !strings.Contains(got.URL, "acme") {
		t.Errorf("url = %q; want the workspace domain injected", got.URL)
	}
}

func TestResources_RejectsMalformedURI(t *testing.T) {
	h := resourcesHarness(t, &fakeResourcesClient{})
	defer h.Close()

	for _, uri := range []string{
		"pipedrive://deals/not-a-number",
		"pipedrive://deals/0",
		"pipedrive://deals/-1",
	} {
		t.Run(uri, func(t *testing.T) {
			if _, err := h.Client.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: uri}); err == nil {
				t.Errorf("%q should not resolve to a record", uri)
			}
		})
	}
}

func TestResources_UpstreamErrorCarriesTheClassTag(t *testing.T) {
	fake := &fakeResourcesClient{err: pipedrive.ErrNotFound}
	h := resourcesHarness(t, fake)
	defer h.Close()

	_, err := h.Client.ReadResource(context.Background(), &mcp.ReadResourceParams{URI: "pipedrive://deals/999"})
	if err == nil {
		t.Fatal("expected an error")
	}
	// A resource read and a tool call should describe the same upstream
	// failure the same way.
	if !strings.Contains(err.Error(), "[not_found]") {
		t.Errorf("err = %q; want the [not_found] class tag", err)
	}
}
