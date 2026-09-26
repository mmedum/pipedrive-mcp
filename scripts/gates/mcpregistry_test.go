package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The registry entry is published once per release and cannot be
// withdrawn, so every field it carries is derived rather than typed,
// and these are the derivations.

func TestGithubRepoStripsTheMajorVersion(t *testing.T) {
	cases := []struct {
		module, owner, repo string
		wantErr             bool
	}{
		{module: "github.com/mmedum/pipedrive-mcp", owner: "mmedum", repo: "pipedrive-mcp"},
		// Go requires the /vN from v2 onwards and it is not part of the
		// repository name. Refusing it would fail the release at the
		// tag, in public, the day this module goes to v2.
		{module: "github.com/mmedum/pipedrive-mcp/v2", owner: "mmedum", repo: "pipedrive-mcp"},
		{module: "github.com/mmedum/pipedrive-mcp/v17", owner: "mmedum", repo: "pipedrive-mcp"},
		// /v1 is not a thing a module path carries, so it is a path
		// segment like any other and the shape is wrong.
		{module: "github.com/mmedum/pipedrive-mcp/v1", wantErr: true},
		{module: "example.invalid/mmedum/thing", wantErr: true},
		{module: "github.com/mmedum", wantErr: true},
	}
	for _, tc := range cases {
		owner, repo, err := githubRepo(tc.module)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%s was accepted as %s/%s", tc.module, owner, repo)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tc.module, err)
			continue
		}
		if owner != tc.owner || repo != tc.repo {
			t.Errorf("%s gave %s/%s, want %s/%s", tc.module, owner, repo, tc.owner, tc.repo)
		}
	}
}

// sums writes a checksum file. This repository's is SHA256SUMS rather
// than checksums.txt, which is the one place this generator differs
// from its siblings — the name is the caller's, so the tests pass a
// path rather than assuming one.
func sums(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "SHA256SUMS")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

const oneBundle = "" +
	"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa  pipedrive-mcp_1.2.0.mcpb\n" +
	"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  pipedrive-mcp-1.2.0-linux-amd64.tar.gz\n"

// TestTheBundleRowMustBeExactlyOne: the hash is what a client verifies
// before installing, so picking the wrong row — or guessing when there
// are two — installs something nobody signed for.
func TestTheBundleRowMustBeExactlyOne(t *testing.T) {
	t.Run("picks the mcpb among other artifacts", func(t *testing.T) {
		name, sum, err := bundleRow(sums(t, oneBundle))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if name != "pipedrive-mcp_1.2.0.mcpb" {
			t.Errorf("name = %q", name)
		}
		if sum != strings.Repeat("a", 64) {
			t.Errorf("sum = %q; it took another row's hash", sum)
		}
	})

	t.Run("refuses two bundles", func(t *testing.T) {
		body := oneBundle +
			"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc  pipedrive-mcp_1.2.1.mcpb\n"
		if _, _, err := bundleRow(sums(t, body)); err == nil {
			t.Fatal("two .mcpb rows were accepted; one of them would have been published")
		}
	})

	t.Run("refuses a checksum file with no bundle", func(t *testing.T) {
		body := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb  pipedrive-mcp-1.2.0-linux-amd64.tar.gz\n"
		_, _, err := bundleRow(sums(t, body))
		if err == nil {
			t.Fatal("a checksum file with no bundle was accepted")
		}
		// The message has to say where to look: the bundle is not an
		// archive goreleaser built, so the usual reason it is absent is
		// that nothing named it in checksum.extra_files.
		if !strings.Contains(err.Error(), "SHA256SUMS") {
			t.Errorf("the error does not name this repository's checksum file: %v", err)
		}
	})

	t.Run("refuses an empty checksum file", func(t *testing.T) {
		if _, _, err := bundleRow(sums(t, "\n\n")); err == nil {
			t.Fatal("a checksum file with no rows was accepted")
		}
	})
}

// TestTheRegistryEntryIsBuiltFromTheRepository: every field is derived,
// so the entry cannot disagree with what shipped.
func TestTheRegistryEntryIsBuiltFromTheRepository(t *testing.T) {
	t.Chdir(repoRoot(t))

	var out bytes.Buffer
	if err := serverJSON("1.2.0", sums(t, oneBundle), &out); err != nil {
		t.Fatalf("serverJSON: %v", err)
	}

	var entry struct {
		Schema      string `json:"$schema"`
		Name        string `json:"name"`
		Description string `json:"description"`
		Version     string `json:"version"`
		Repository  struct {
			URL    string `json:"url"`
			Source string `json:"source"`
		} `json:"repository"`
		Packages []struct {
			RegistryType string `json:"registryType"`
			Identifier   string `json:"identifier"`
			FileSHA256   string `json:"fileSha256"`
			Version      string `json:"version"`
			Transport    struct {
				Type string `json:"type"`
			} `json:"transport"`
		} `json:"packages"`
	}
	if err := json.Unmarshal(out.Bytes(), &entry); err != nil {
		t.Fatalf("the entry is not valid JSON: %v\n%s", err, out.String())
	}

	// Stated, not compared to registrySchema: reading the constant the
	// generator emits asserts that it equals itself, and a wrong URL in
	// that constant would pass.
	const wantSchema = "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json"
	if entry.Schema != wantSchema {
		t.Errorf("$schema = %q; want %q", entry.Schema, wantSchema)
	}
	if entry.Name != "io.github.mmedum/pipedrive-mcp" {
		t.Errorf("name = %q; it is derived from the module path", entry.Name)
	}
	if entry.Version != "1.2.0" {
		t.Errorf("version = %q; the v belongs to the tag, not the entry", entry.Version)
	}
	if entry.Repository.Source != "github" {
		t.Errorf("repository.source = %q", entry.Repository.Source)
	}
	if len(entry.Packages) != 1 {
		t.Fatalf("the entry carries %d packages; want exactly the bundle", len(entry.Packages))
	}
	pkg := entry.Packages[0]
	if pkg.RegistryType != "mcpb" {
		t.Errorf("registryType = %q", pkg.RegistryType)
	}
	if pkg.FileSHA256 != strings.Repeat("a", 64) {
		t.Errorf("fileSha256 = %q; it must be the bundle's row", pkg.FileSHA256)
	}
	if !strings.Contains(pkg.Identifier, "pipedrive-mcp_1.2.0.mcpb") {
		t.Errorf("identifier = %q; it must point at the published bundle", pkg.Identifier)
	}
	if !strings.HasPrefix(pkg.Identifier, "https://github.com/mmedum/pipedrive-mcp/releases/download/v1.2.0/") {
		t.Errorf("identifier = %q; it must be the release download URL for this tag", pkg.Identifier)
	}
	if pkg.Transport.Type != "stdio" {
		t.Errorf("transport = %q; this server speaks stdio", pkg.Transport.Type)
	}

	// The description is the manifest's, and the registry caps it. A
	// sibling's was 148 characters and would have failed its first
	// publish, which cannot be retried against a version that exists.
	//
	// 100 is written out rather than read from descriptionMax, because
	// the cap is the registry's and not ours: a wrong value in our
	// constant is exactly the defect worth catching, and comparing to
	// it would pass.
	const registryCap = 100
	if entry.Description == "" || len(entry.Description) > registryCap {
		t.Errorf("description is %d characters; the registry allows %d",
			len(entry.Description), registryCap)
	}
}

// TestTheVersionMayCarryItsVOrNot: the workflow passes a tag and a
// person passes what they remember.
func TestTheVersionMayCarryItsVOrNot(t *testing.T) {
	t.Chdir(repoRoot(t))

	var withV, without bytes.Buffer
	if err := serverJSON("v1.2.0", sums(t, oneBundle), &withV); err != nil {
		t.Fatalf("v-prefixed: %v", err)
	}
	if err := serverJSON("1.2.0", sums(t, oneBundle), &without); err != nil {
		t.Fatalf("bare: %v", err)
	}
	if withV.String() != without.String() {
		t.Error("v1.2.0 and 1.2.0 produced different entries")
	}
}

// ------------------------------------------- registry schema conformance
//
// A registry entry cannot be withdrawn once published, so "it failed in
// somebody else's tool" is not a recoverable outcome. serverJSON now
// holds the entry against the schema it cites before printing it; these
// watch that check fail, the way the rest of this package's checks are.

func registrySchemaBytes(t *testing.T) []byte {
	t.Helper()
	data, err := os.ReadFile(repoPath(t, registrySchemaPath))
	if err != nil {
		t.Fatalf("read %s: %v", registrySchemaPath, err)
	}
	return data
}

// The vendored bytes are the bytes the constant names.
func TestRegistrySchemaMatchesItsRecordedHash(t *testing.T) {
	if got := schemaSHA256(registrySchemaBytes(t)); got != registrySchemaSHA256 {
		t.Errorf("%s hashes to %s; the recorded hash is %s", registrySchemaPath, got, registrySchemaSHA256)
	}
}

func TestRegistryEntrySchemaCatchesABrokenEntry(t *testing.T) {
	schema := registrySchemaBytes(t)

	// A entry that is valid, as the shape serverJSON emits.
	good := map[string]any{
		"$schema":     registrySchema,
		"name":        "io.github.example/thing",
		"description": "A server.",
		"version":     "1.0.0",
	}
	if err := validateDocument(good, schema); err != nil {
		t.Fatalf("a valid entry was refused: %v", err)
	}

	cases := []struct {
		name   string
		breaks func(map[string]any)
		want   string
	}{
		{"a missing required field", func(d map[string]any) { delete(d, "version") }, "version"},
		{"a field of the wrong type", func(d map[string]any) { d["name"] = 42 }, "name"},
		// The registry caps the description at 100 characters. The gate
		// has its own descriptionMax for a better message; this is the
		// authority it was copied from, and the two can now disagree
		// only loudly.
		{"an over-long description", func(d map[string]any) {
			d["description"] = strings.Repeat("x", descriptionMax+1)
		}, "description"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			entry := map[string]any{}
			for k, v := range good {
				entry[k] = v
			}
			tc.breaks(entry)
			err := validateDocument(entry, schema)
			if err == nil {
				t.Fatal("a broken entry satisfied the schema")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal does not say %q: %v", tc.want, err)
			}
		})
	}
}
