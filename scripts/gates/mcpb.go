package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// The bundle gate and the packer (§12, and the standard's §10b).
//
// They are two subcommands because the useful half needs no build. The
// checks below are REFERENTIAL — they ask whether the manifest's names
// are the names something stages — and the staged names are static, so
// they can run on every commit against the committed manifest. Only the
// packing waits for a release, where the binaries exist.
//
// A schema would not catch any of this. Every failure here produces a
// manifest that is well formed, packs, installs, and then does nothing:
// an entry point nobody staged, an override for a platform the bundle
// does not claim, a platform whose command is another platform's binary,
// a ${user_config.x} that was never declared, a launcher choosing
// between names the packer does not write. Those are facts about the
// archive rather than about the document.

// manifestPath is the committed manifest, which carries a placeholder
// version so a manifest in the tree cannot claim a stale one.
const manifestPath = "packaging/mcpb/manifest.json"

// placeholderVersion is what the committed manifest must say. The packer
// refuses any other value and writes the real version through a JSON
// decode and encode, never a substitution over text.
const placeholderVersion = "0.0.0-dev"

// launcherName is the Linux launcher inside the bundle. It picks a
// binary by architecture and execs it.
//
// The script is GENERATED from bundleFiles at pack time rather than
// committed beside the manifest. A committed launcher is a second list
// of binary names, and a second list can disagree with the first — so
// the gate had to read the script back and compare the two. Generating
// it makes the disagreement unrepresentable instead of detected.
const launcherName = "server/launch-linux.sh"

// staged is one file the packer puts in the bundle.
//
// This list is the single source of the staged NAMES. The gate reads it
// without building anything, and the packer reads the same rows to find
// the files — so a manifest naming a file nobody stages is a fact about
// two files in this repository, found on the commit that wrote it.
type staged struct {
	// path is where the file lands inside the bundle.
	path string
	// glob finds the file under the release's dist directory. Empty for
	// a file this repository ships itself.
	glob string
	// source is a file in this repository, for the launcher.
	source string
	// platform is the one this file is the entry point for, empty for a
	// file a launcher chooses rather than the manifest.
	platform string
	// launched marks a binary the Linux launcher picks between. Those
	// names appear in a shell script no manifest mentions, so nothing
	// else can check them.
	launched bool
	// uname is the `uname -m` values the launcher maps to this binary.
	// Only a launched row has them, and they are the launcher's whole
	// input besides the name.
	uname []string
	// generated marks a file this packer writes rather than copies.
	generated bool
}

// bundleFiles is what a bundle contains. The macOS slot is goreleaser's
// universal binary: a manifest has no key for the architecture, so every
// platform it claims has to work on both.
//
// The universal glob reads "universal then darwin", which looks backwards
// and is not: goreleaser names that directory <id>_darwin_all, so the id
// comes FIRST. Written the obvious way round it matched nothing, and the
// packer would have failed on the one file the macOS half of the bundle
// is. `gates release` holds every glob here against the build matrix in
// .goreleaser.yaml so neither can drift from the other again.
var bundleFiles = []staged{
	{path: "server/pipedrive-mcp-darwin", glob: "dist/*universal*darwin*/pipedrive-mcp", platform: "darwin"},
	{path: "server/pipedrive-mcp.exe", glob: "dist/*windows_amd64*/pipedrive-mcp.exe", platform: "win32"},
	{path: launcherName, platform: "linux", generated: true},
	{path: "server/pipedrive-mcp-linux-x64", glob: "dist/*linux_amd64*/pipedrive-mcp",
		launched: true, uname: []string{"x86_64", "amd64"}},
	{path: "server/pipedrive-mcp-linux-arm64", glob: "dist/*linux_arm64*/pipedrive-mcp",
		launched: true, uname: []string{"aarch64", "arm64"}},
}

// manifest is the part of the document these checks are about.
type manifest struct {
	Schema          string `json:"$schema"`
	ManifestVersion string `json:"manifest_version"`
	Support         string `json:"support"`
	Name            string `json:"name"`
	Version         string `json:"version"`
	License         string `json:"license"`
	Description     string `json:"description"`
	LongDescription string `json:"long_description"`
	Server          struct {
		Type       string `json:"type"`
		EntryPoint string `json:"entry_point"`
		MCPConfig  struct {
			Command           string            `json:"command"`
			Args              []string          `json:"args"`
			Env               map[string]string `json:"env"`
			PlatformOverrides map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
			} `json:"platform_overrides"`
		} `json:"mcp_config"`
	} `json:"server"`
	UserConfig    map[string]json.RawMessage `json:"user_config"`
	Compatibility struct {
		Platforms []string `json:"platforms"`
	} `json:"compatibility"`
}

// mcpbGate runs the referential checks against the committed manifest.
func mcpbGate() error {
	m, document, err := readManifestBoth(manifestPath)
	if err != nil {
		return err
	}
	problems := validateManifest(m, bundleFiles, launcherNames())
	if err := checkAgainstSchema(document); err != nil {
		problems = append(problems, err.Error())
	}

	// The version placeholder, so a committed manifest cannot claim a
	// version that shipped.
	if m.Version != placeholderVersion {
		problems = append(problems, fmt.Sprintf(
			"the committed manifest says version %q; it must say %q and the packer writes the real one",
			m.Version, placeholderVersion))
	}
	// The first-run requirement, said in the manifest because it is the
	// one a user meets.
	//
	// This differs from the Google servers deliberately. Theirs cannot
	// log anybody in — OAuth needs a Desktop client the user creates and
	// a login from a terminal — so their manifests have to say so. This
	// one CAN be fully configured from the bundle: Pipedrive issues a
	// per-user API token and it goes in user_config. What a user needs
	// instead is to know WHERE that token comes from, so that is what is
	// held here. Copying the OAuth sentence across would have been a
	// false statement passing a check.
	if !strings.Contains(strings.ToLower(m.LongDescription), "api token") {
		problems = append(problems, "long_description does not say the user needs a Pipedrive API token "+
			"or where to get one, which is the first thing that stops somebody who installs it")
	}
	if want := licenceOf(); want != "" && m.License != want {
		problems = append(problems, fmt.Sprintf(
			"the manifest says the licence is %q and LICENSE is %s", m.License, want))
	}
	if len(problems) > 0 {
		sort.Strings(problems)
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}

	fmt.Printf("  %s: %d staged files, %d platforms, %d user_config keys: every name resolves, and the document satisfies %s\n",
		manifestPath, len(bundleFiles), len(m.Compatibility.Platforms), len(m.UserConfig),
		vendoredSchemaPath(m.ManifestVersion))
	return nil
}

// userConfigRef matches every ${user_config.x} in a value, not only a
// value that is nothing but one: a composed "${user_config.dir}/sub"
// would otherwise skip the check and start the server with the variable
// unsubstituted.
var userConfigRef = regexp.MustCompile(`\$\{user_config\.([A-Za-z0-9_]+)\}`)

// validateManifest is the checks, as one function so a test can run
// them against a broken manifest without touching the repository.
//
// It takes the staged list and the launcher's names as arguments for the
// same reason: each check is watched failing before it is believed, and
// a check that could only be exercised by breaking the real manifest
// would not be.
func validateManifest(m manifest, files []staged, launcher []string) []string {
	stagedPaths := map[string]staged{}
	for _, f := range files {
		stagedPaths[f.path] = f
	}
	commands := map[string]string{"": m.Server.MCPConfig.Command}
	for platform, over := range m.Server.MCPConfig.PlatformOverrides {
		if over.Command != "" {
			commands[platform] = over.Command
		}
	}

	problems := make([]string, 0, 8)
	problems = append(problems, checkEntryAndCommands(m, stagedPaths, commands)...)
	problems = append(problems, checkUserConfigRefs(m)...)
	problems = append(problems, checkPlatforms(m, stagedPaths, commands)...)
	problems = append(problems, checkLauncherNames(files, launcher)...)
	problems = append(problems, checkManifestShape(m)...)
	return problems
}

// schemaRef is the mcpb release whose schema files this manifest is
// held against.
//
// A tag, not main. The version in the PATH pins the format; the ref
// pins the BYTES, and main's bytes can change under a path that still
// reads as pinned. v2.1.2's copies are byte-identical to main's today —
// which is the argument for the tag rather than against it, because
// nothing would tell us when that stopped being true.
const schemaRef = "v2.1.2"

// minManifestVersion is the oldest manifest shape this repository will
// ship.
//
// Without a floor, checkManifestShape is satisfied by any version that
// agrees with its own $schema — which is exactly what a stale but
// self-consistent 0.2 manifest is, and three of the seven sibling
// repositories were sitting on one. 0.3 rather than 0.4 on purpose:
// upstream serves 0.2, 0.3 and 0.4, and the only difference between 0.3
// and 0.4 is a "uv" value added to the server.type enum. This server is
// type "binary", so 0.4 buys nothing and claiming it would be a version
// number chosen for being larger.
const minManifestVersion = "0.3"

// schemaFor is the pinned schema URL for a manifest version.
//
// Pinned, not the /dist/ path that serves whatever is current: this
// repository has a whole gate about "latest" drifting, and a manifest
// is the one file that states its own version AND validates against a
// URL, so the two can disagree silently. Three of the seven sibling
// repositories sat on manifest_version 0.2 while pointing at the
// unpinned URL, which by then served 0.3, and nothing anywhere said so.
func schemaFor(manifestVersion string) string {
	return "https://raw.githubusercontent.com/anthropics/mcpb/" + schemaRef +
		"/schemas/mcpb-manifest-v" + manifestVersion + ".schema.json"
}

// olderThan compares two dotted version strings numerically, so "0.10"
// is newer than "0.9" rather than sorting before it.
func olderThan(got, floor string) bool {
	gotParts, floorParts := strings.Split(got, "."), strings.Split(floor, ".")
	for i := 0; i < len(gotParts) || i < len(floorParts); i++ {
		g, f := 0, 0
		if i < len(gotParts) {
			g, _ = strconv.Atoi(gotParts[i])
		}
		if i < len(floorParts) {
			f, _ = strconv.Atoi(floorParts[i])
		}
		if g != f {
			return g < f
		}
	}
	return false
}

// checkManifestShape: the document agrees with the schema it cites, and
// says where to report a problem.
//
// Neither field changes what the packer does, which is exactly why they
// drift — the bundle builds and installs either way, and the mismatch
// only shows up as a validation failure in somebody else's tool.
func checkManifestShape(m manifest) []string {
	var problems []string
	if olderThan(m.ManifestVersion, minManifestVersion) {
		problems = append(problems, fmt.Sprintf(
			"manifest_version is %q and this repository ships %q or newer; a manifest that agrees "+
				"with its own $schema is still stale if both are old",
			m.ManifestVersion, minManifestVersion))
	}
	switch {
	case m.Schema == "":
		problems = append(problems, fmt.Sprintf("no $schema; manifest_version %q should cite %s",
			m.ManifestVersion, schemaFor(m.ManifestVersion)))
	case m.Schema != schemaFor(m.ManifestVersion):
		problems = append(problems, fmt.Sprintf("$schema is %q but manifest_version %q wants %s",
			m.Schema, m.ManifestVersion, schemaFor(m.ManifestVersion)))
	}
	if strings.TrimSpace(m.Support) == "" {
		problems = append(problems, "no support URL; an installed bundle is the one copy of this "+
			"server with no repository around it to find one in")
	}
	return problems
}

// The schema half of the manifest checks.
//
// checkManifestShape above asks whether the DECLARATION is right: that
// $schema agrees with manifest_version and that neither is stale. This
// asks the other half — whether the document actually satisfies the
// schema it cites. Neither substitutes for the other, and neither
// substitutes for the referential checks at the top of this file: a
// manifest can satisfy the schema and still name an entry point nobody
// stages, which is why that comment says a schema would not catch any
// of THOSE. It catches a different set, including every unknown or
// misspelled top-level key, because the schema closes the root with
// additionalProperties: false.
//
// The schema is VENDORED rather than fetched. A gate that needs the
// network fails on somebody else's bad day, and `make check` runs
// offline. But a vendored copy is only worth its provenance — the
// sibling google-chat-mcp validates against a vendored copy and never
// checks that the cited URL agrees with it, which is the exact mirror of
// the gap this closes — so the bytes are pinned by hash here while the
// URL is pinned by schemaFor.
//
// What the hash proves is bounded, and worth stating: that these bytes
// are the ones somebody reviewed, not that they still match what the URL
// serves. Nothing here can tell you upstream retagged — only a re-fetch
// can, which is why re-pinning is a step in docs/release.md beside the
// schemaRef bump rather than a promise made in a comment.

// vendoredSchemaSHA256 is the SHA256 of the vendored schema for each
// manifest version. See the block comment above for what it buys.
var vendoredSchemaSHA256 = map[string]string{
	"0.3": "3a0ac9d845711a1b9b17dfa5a52f8b60628239d6a86a9db417206a9efc78592d",
}

// vendoredSchemaPath is where the copy for a manifest version lives,
// named after the version so bumping manifest_version without vendoring
// the matching schema fails loudly rather than validating against the
// old shape.
func vendoredSchemaPath(manifestVersion string) string {
	return "packaging/mcpb/mcpb-manifest-v" + manifestVersion + ".schema.json"
}

// checkAgainstSchema validates a decoded manifest against the vendored
// copy of the schema its own manifest_version names.
func checkAgainstSchema(document map[string]any) error {
	version, _ := document["manifest_version"].(string)
	if version == "" {
		return fmt.Errorf("no manifest_version, so there is no schema to check the document against")
	}
	want, ok := vendoredSchemaSHA256[version]
	if !ok {
		return fmt.Errorf(
			"manifest_version is %q and no copy of its schema is vendored; fetch %s into %s and record its SHA256 in vendoredSchemaSHA256",
			version, schemaFor(version), vendoredSchemaPath(version))
	}
	return validateAgainstVendored(document, vendoredSchemaPath(version), want, schemaFor(version))
}

// validateAgainstVendored holds a document against this repository's copy
// of a schema, having first checked the copy is the one that was
// reviewed. Two callers: the bundle manifest and the registry entry,
// which cite different schemas and have the same gap without it.
func validateAgainstVendored(document any, path, wantSHA256, sourceURL string) error {
	data, err := os.ReadFile(path) //nolint:gosec // a repository path from a constant or a pinned version
	if err != nil {
		return fmt.Errorf("cannot read the vendored schema: %w", err)
	}
	if got := schemaSHA256(data); got != wantSHA256 {
		return fmt.Errorf("%s hashes to %s and this repository records %s; it is no longer the reviewed copy of %s",
			path, got, wantSHA256, sourceURL)
	}
	return validateDocument(document, data)
}

func schemaSHA256(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// validateDocument validates one decoded document against one schema.
//
// Split from the loading above so a test can hold a real schema against
// a broken document without a file on disk — every check in this file is
// watched failing before it is believed.
func validateDocument(document any, schemaJSON []byte) error {
	var schema jsonschema.Schema
	if err := json.Unmarshal(schemaJSON, &schema); err != nil {
		return fmt.Errorf("the vendored schema is not valid JSON Schema: %w", err)
	}
	// Resolve(nil) does no network fetch: every $ref in this schema is a
	// local fragment. Worth knowing that the schema declares draft-07
	// while this library implements 2020-12 — the constraints that carry
	// the weight here (additionalProperties, required, enum) mean the
	// same in both, but a future upstream schema leaning on draft-07's
	// $ref sibling semantics could validate differently than its
	// publisher intended.
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return fmt.Errorf("the vendored schema does not resolve: %w", err)
	}
	if err := resolved.Validate(document); err != nil {
		return fmt.Errorf("the manifest does not satisfy the schema it cites: %w", err)
	}
	return nil
}

// readManifestDocument decodes the manifest as a plain JSON value, which
// is what a schema validates. The typed readManifest cannot stand in:
// decoding into a struct silently drops every key the struct does not
// declare, and those keys are most of what a schema is there to check.
// readManifestBoth reads the manifest once and decodes it twice.
//
// Both shapes are needed and they answer different questions: the struct
// for field access, the plain document for the schema. The typed decode
// cannot stand in for the document — it silently drops every key the
// struct does not declare, and those keys are most of what a schema is
// there to check. But there is no reason to read the file, or spell the
// error, twice to get them.
func readManifestBoth(path string) (manifest, map[string]any, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a repository path from a constant
	if err != nil {
		return manifest{}, nil, err
	}
	var m manifest
	var document map[string]any
	for _, into := range []any{&m, &document} {
		if err := json.Unmarshal(data, into); err != nil {
			return manifest{}, nil, fmt.Errorf("%s is not valid JSON: %w", path, err)
		}
	}
	return m, document, nil
}

// resolveStaged strips the ${__dirname} a manifest command carries.
func resolveStaged(cmd string) string {
	return strings.TrimPrefix(strings.TrimPrefix(cmd, "${__dirname}"), "/")
}

// checkEntryAndCommands: entry_point and every command name a file the
// packer actually stages.
func checkEntryAndCommands(m manifest, stagedPaths map[string]staged, commands map[string]string) []string {
	var problems []string
	if _, ok := stagedPaths[m.Server.EntryPoint]; !ok {
		problems = append(problems, fmt.Sprintf("entry_point %q is not a file the packer stages",
			m.Server.EntryPoint))
	}
	for platform, cmd := range commands {
		where := "mcp_config.command"
		if platform != "" {
			where = "platform_overrides." + platform + ".command"
		}
		if _, ok := stagedPaths[resolveStaged(cmd)]; !ok {
			problems = append(problems, fmt.Sprintf("%s is %q, which the packer does not stage", where, cmd))
		}
	}
	return problems
}

// checkUserConfigRefs: every ${user_config.x} an env value spends is a
// declared key. Every reference in the value, not only a value that is
// nothing but one — a composed "${user_config.dir}/sub" would otherwise
// skip the check and start the server with the variable unsubstituted.
func checkUserConfigRefs(m manifest) []string {
	envs := map[string]map[string]string{"mcp_config": m.Server.MCPConfig.Env}
	for platform, over := range m.Server.MCPConfig.PlatformOverrides {
		envs["platform_overrides."+platform] = over.Env
	}
	var problems []string
	for where, env := range envs {
		for key, value := range env {
			for _, ref := range userConfigRef.FindAllStringSubmatch(value, -1) {
				if _, declared := m.UserConfig[ref[1]]; !declared {
					problems = append(problems, fmt.Sprintf(
						"%s.env.%s spends ${user_config.%s}, which user_config does not declare",
						where, key, ref[1]))
				}
			}
		}
	}
	return problems
}

// checkPlatforms: every override names a platform the bundle claims, and
// every claimed platform spawns the file staged FOR it.
//
// Deleting a win32 override passes every other check: Windows then runs
// the default command, which is the macOS universal binary, and that
// file really is in the bundle.
func checkPlatforms(m manifest, stagedPaths map[string]staged, commands map[string]string) []string {
	claimed := map[string]bool{}
	for _, p := range m.Compatibility.Platforms {
		claimed[p] = true
	}
	var problems []string
	for platform := range m.Server.MCPConfig.PlatformOverrides {
		if !claimed[platform] {
			problems = append(problems, fmt.Sprintf(
				"platform_overrides has %q, which compatibility.platforms does not claim", platform))
		}
	}
	for _, platform := range m.Compatibility.Platforms {
		cmd, ok := commands[platform]
		if !ok {
			cmd = commands[""]
		}
		file, found := stagedPaths[resolveStaged(cmd)]
		if found && file.platform != platform {
			problems = append(problems, fmt.Sprintf(
				"platform %q runs %q, which is staged for %q", platform, file.path, file.platform))
		}
	}
	return problems
}

// checkLauncherNames: the launcher chooses between the packer's own
// names. They live in a shell script no manifest mentions, so nothing
// else can see them: renaming a staged binary otherwise passes every
// check and ships a bundle that fails for every user of that platform.
func checkLauncherNames(files []staged, launcher []string) []string {
	var launchable []string
	for _, f := range files {
		if f.launched {
			launchable = append(launchable, filepath.Base(f.path))
		}
	}
	sort.Strings(launchable)
	chosen := append([]string{}, launcher...)
	sort.Strings(chosen)
	if strings.Join(launchable, " ") != strings.Join(chosen, " ") {
		return []string{fmt.Sprintf("the launcher runs [%s]; the packer stages [%s]",
			strings.Join(chosen, " "), strings.Join(launchable, " "))}
	}
	return nil
}

// launcherBinary matches a binary name the launcher execs.
var launcherBinary = regexp.MustCompile(`exec "\$dir/([A-Za-z0-9._-]+)"`)

// launcherScript generates the Linux launcher from bundleFiles.
//
// Claude Desktop's manifest names a command per PLATFORM and has no key
// for the architecture, and Linux ships both x64 and arm64. So this
// picks the binary and EXECS it: the server talks MCP over this
// process's stdio, and a shell left in the middle would own the pipes.
//
// Generated rather than committed, because the names it dispatches to
// are the packer's. Held in a file of its own they were a second list
// that could drift from the first, visible to nothing — a manifest
// never mentions this script's contents, so a renamed binary packed
// cleanly, installed cleanly, and failed for every Linux user.
func launcherScript() string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n" +
		"# Generated by `gates mcpb-pack` from bundleFiles. Do not edit.\n" +
		"#\n" +
		"# A manifest names a command per platform and has no key for the\n" +
		"# architecture, so the Linux entry would otherwise have to be one\n" +
		"# binary and be wrong for everyone else. macOS solves this with a\n" +
		"# universal binary and Windows by running amd64 under emulation;\n" +
		"# Linux has neither, so the choice is made here.\n" +
		"set -eu\n\n" +
		`dir=$(dirname "$0")` + "\n\n" +
		`case "$(uname -m)" in` + "\n")
	for _, f := range bundleFiles {
		if !f.launched {
			continue
		}
		fmt.Fprintf(&b, "  %s)\n    exec \"$dir/%s\" \"$@\"\n    ;;\n",
			strings.Join(f.uname, " | "), filepath.Base(f.path))
	}
	b.WriteString("esac\n\n" +
		"# Never stdout: a line of English there corrupts the JSON-RPC stream\n" +
		"# before the client's first request completes, and the client reports\n" +
		"# it as a protocol error rather than as this.\n" +
		`echo "pipedrive-mcp: no binary in this bundle for $(uname -m); builds for ` +
		`x86_64 and aarch64 are at https://github.com/mmedum/pipedrive-mcp/releases" >&2` + "\n" +
		"exit 1\n")
	return b.String()
}

// launcherNames reads the binaries the Linux launcher chooses between.
//
// It parses the generated script rather than trusting the table it was
// generated from, so checkLauncherNames still compares two things: a
// generator that skipped a row fails here rather than shipping a
// launcher that cannot reach it.
func launcherNames() []string {
	return launcherNamesIn(launcherScript())
}

// launcherNamesIn is the extraction itself, so the test that proves the
// launcher and the packer agree runs the gate's own code rather than a
// second copy of the regexp loop.
func launcherNamesIn(body string) []string {
	found := launcherBinary.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, len(found))
	for _, m := range found {
		out = append(out, m[1])
	}
	return out
}

func readManifest(path string) (manifest, error) {
	m, _, err := readManifestBoth(path)
	return m, err
}

// licenceOf reads the SPDX identifier the repository's LICENSE file
// carries, so the manifest cannot claim a different one.
//
// It is a referential check like the others: "MIT" in a manifest beside
// an Apache licence is well formed, packs, installs, and is wrong about
// the one thing a redistributor reads it for. The first draft of this
// manifest said MIT.
func licenceOf() string {
	data, err := os.ReadFile("LICENSE")
	if err != nil {
		return ""
	}
	head := string(data)
	switch {
	case strings.Contains(head, "Apache License"):
		return "Apache-2.0"
	case strings.Contains(head, "MIT License"):
		return "MIT"
	default:
		return ""
	}
}

// The three entry points, in this repository's command signature.

func mcpbCmd(_ io.Writer, _ []string) error {
	return mcpbGate()
}

func mcpbPackCmd(_ io.Writer, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: gates mcpb-pack DIST VERSION OUT")
	}
	return packMCPB(args[0], args[1], args[2])
}
