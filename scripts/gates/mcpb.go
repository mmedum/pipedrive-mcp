package main

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"github.com/mmedum/pipedrive-mcp/internal/config"
	"github.com/mmedum/pipedrive-mcp/internal/server"
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
	// source is a file in this repository: the license and the README.
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

// program reports whether the bundle runs the file: an entry point or a
// binary the launcher picks. The license and the README are packed to
// be read, and nothing may spawn them.
func (f staged) program() bool { return f.platform != "" || f.launched }

// mode is the mode a staged file is packed with. What runs is 0755,
// because the desktop spawns it with the mode the zip gives it. The
// license and the README are read, and are 0644.
func (f staged) mode() os.FileMode {
	if f.program() {
		return 0o755
	}
	return 0o644
}

// bundleFiles is what a bundle contains, in the family's layout: Go's
// architecture names, as in the release archives. The macOS slot is
// goreleaser's universal binary: a manifest has no key for the
// architecture, so every platform it claims has to work on both.
//
// The universal glob reads "universal then darwin", which looks backwards
// and is not: goreleaser names that directory <id>_darwin_all, so the id
// comes FIRST. Written the obvious way round it matched nothing, and the
// packer would have failed on the one file the macOS half of the bundle
// is.
var bundleFiles = []staged{
	{path: "server/pipedrive-mcp-darwin", glob: "dist/*universal*darwin*/pipedrive-mcp", platform: "darwin"},
	{path: "server/pipedrive-mcp.exe", glob: "dist/*windows_amd64*/pipedrive-mcp.exe", platform: "win32"},
	{path: launcherName, platform: "linux", generated: true},
	{path: "server/pipedrive-mcp-linux-amd64", glob: "dist/*linux_amd64*/pipedrive-mcp",
		launched: true, uname: []string{"x86_64", "amd64"}},
	{path: "server/pipedrive-mcp-linux-arm64", glob: "dist/*linux_arm64*/pipedrive-mcp",
		launched: true, uname: []string{"aarch64", "arm64"}},
	// The license, because the bundle redistributes the binaries as the
	// archives do, and the README, as in the archives.
	{path: "LICENSE", source: "LICENSE"},
	{path: "README.md", source: "README.md"},
}

// manifest is the part of the document these checks are about.
type manifest struct {
	Schema          string   `json:"$schema"`
	ManifestVersion string   `json:"manifest_version"`
	Support         string   `json:"support"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	License         string   `json:"license"`
	Description     string   `json:"description"`
	LongDescription string   `json:"long_description"`
	PrivacyPolicies []string `json:"privacy_policies"`
	Server          struct {
		Type       string `json:"type"`
		EntryPoint string `json:"entry_point"`
		MCPConfig  struct {
			Command string `json:"command"`
			// Args is raw, so that a key set to null is told apart from
			// one that is missing.
			Args              json.RawMessage   `json:"args"`
			Env               map[string]string `json:"env"`
			PlatformOverrides map[string]struct {
				Command string            `json:"command"`
				Args    []string          `json:"args"`
				Env     map[string]string `json:"env"`
			} `json:"platform_overrides"`
		} `json:"mcp_config"`
	} `json:"server"`
	UserConfig    map[string]userConfigOption `json:"user_config"`
	Compatibility struct {
		ClaudeDesktop string   `json:"claude_desktop"`
		Platforms     []string `json:"platforms"`
	} `json:"compatibility"`
}

// mcpbGate runs every check against the committed manifest under root,
// on the names the packer stages.
func mcpbGate(root string) error {
	_, m, problems, err := committedManifest(root)
	if err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	fmt.Printf("  %s: %d staged files, %d platforms, %d user_config keys: every name resolves, and the document satisfies %s\n",
		manifestPath, len(bundleFiles), len(m.Compatibility.Platforms), len(m.UserConfig),
		vendoredSchemaPath(m.ManifestVersion))
	return nil
}

// committedManifest is where the gate, the packer and the registry
// entry all start: the committed manifest under root, read once and
// decoded once into the struct the checks read and once into the
// document the schema check and the packer's stamp read, and every
// problem manifestProblems finds in it. The packer ships document with
// the real version stamped in and encoded again, not the committed
// bytes, and holds what it stamps to the vendored schema too.
//
// A key repeated in one object is refused before anything else is
// checked, and so is one that differs from another only in case. The
// decode into manifest merges a repeated object into the first and
// matches a key in any case, while the schema validator, the packer's
// encode and the host each keep the last or keep both, so every other
// check would read a document that is not the one shipped.
func committedManifest(root string) (document map[string]any, m manifest, problems []string, err error) {
	raw, err := os.ReadFile(filepath.Join(root, manifestPath)) //nolint:gosec // a repository path from a constant
	if err != nil {
		return nil, manifest{}, nil, err
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		return nil, manifest{}, []string{fmt.Sprintf("the manifest does not decode: %v", err)}, nil
	}
	repeated, err := repeatedKeys(raw)
	if err != nil {
		return nil, manifest{}, []string{fmt.Sprintf("the manifest's keys cannot be read: %v", err)}, nil
	}
	if len(repeated) > 0 {
		for _, at := range repeated {
			problems = append(problems, fmt.Sprintf("the manifest has %s more than once, case aside; the gate's "+
				"decode merges a repeated key and matches one in any case, while the schema, the packer and the "+
				"host keep the last or keep both, so they would read different documents", at))
		}
		return nil, manifest{}, problems, nil
	}
	if err = json.Unmarshal(raw, &document); err != nil {
		return nil, manifest{}, []string{fmt.Sprintf("the manifest does not decode: %v", err)}, nil
	}
	return document, m, manifestProblems(document, m, bundleFiles, root), nil
}

// repeatedKeys is the path of every key an object in raw has more than
// once, each once, in document order, counting two keys that differ
// only in case as one: Go's decode into a struct matches a key in any
// case. The path is "server" at the top level,
// "server.mcp_config.env.PIPEDRIVE_DRY_RUN" inside, [i] for an element
// of a list, and ["a.b"] for a key that holds a dot or a bracket, so no
// two keys share a path. A document whose tokens cannot be walked is an
// error rather than one with no repeats.
func repeatedKeys(raw []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	var repeated []string
	var walk func(path string) error
	walk = func(path string) error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'):
			// classes is each key in the object with how many keys fold
			// to it.
			type class struct {
				key string
				n   int
			}
			var classes []class
			for dec.More() {
				keyTok, keyErr := dec.Token()
				if keyErr != nil {
					return keyErr
				}
				key, _ := keyTok.(string)
				at := keyPath(path, key)
				i := slices.IndexFunc(classes, func(c class) bool { return strings.EqualFold(c.key, key) })
				if i < 0 {
					classes = append(classes, class{key: key})
					i = len(classes) - 1
				}
				classes[i].n++
				if classes[i].n == 2 {
					repeated = append(repeated, at)
				}
				err = walk(at)
				if err != nil {
					return err
				}
			}
		case json.Delim('['):
			for i := 0; dec.More(); i++ {
				err = walk(fmt.Sprintf("%s[%d]", path, i))
				if err != nil {
					return err
				}
			}
		default:
			return nil
		}
		_, err = dec.Token() // the closing brace or bracket
		return err
	}
	if err := walk(""); err != nil {
		return nil, err
	}
	return repeated, nil
}

// keyPath is the path of key inside the object at path: joined by a dot,
// or quoted in brackets when the key holds a dot or a bracket itself.
func keyPath(path, key string) string {
	if strings.ContainsAny(key, ".[]") {
		return path + "[" + strconv.Quote(key) + "]"
	}
	if path == "" {
		return key
	}
	return path + "." + key
}

// manifestProblems is every check the gate and the packer both run, so
// the release packs no manifest the gate would refuse: the placeholder
// version, the API token sentence, the manifest's own declaration and
// the vendored schema it cites, the referential checks against the
// staged names, the launcher's names, what the bundle says it is, the
// settings it asks for, and the LICENSE beside it. document is the
// manifest decoded as JSON and m the same decoded into the struct, files
// the staged layout, and root the repository the files beside the
// manifest are read from. Sorted, so one run reads the same as the next.
func manifestProblems(document map[string]any, m manifest, files []staged, root string) []string {
	problems := validateManifest(m, files)
	problems = append(problems, checkLauncherNames(files, launcherNames())...)

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
	problems = append(problems, checkManifestShape(m)...)
	problems = append(problems, identityProblems(m, files)...)
	problems = append(problems, settingProblems(m, bundleLeftOut)...)

	// And against the schema the manifest cites, which the checks above
	// only name.
	if err := checkAgainstSchema(root, document); err != nil {
		problems = append(problems, err.Error())
	}

	switch want, err := licenseOf(root); {
	case err != nil:
		problems = append(problems, err.Error())
	case want != "" && m.License != want:
		problems = append(problems, fmt.Sprintf(
			"the manifest says the license is %q and LICENSE is %s", m.License, want))
	}
	sort.Strings(problems)
	return problems
}

// userConfigRef matches every ${user_config.x} in a value, not only a
// value that is nothing but one: a composed "${user_config.dir}/sub"
// would otherwise skip the check and start the server with the variable
// unsubstituted.
var userConfigRef = regexp.MustCompile(`\$\{user_config\.([A-Za-z0-9_]+)\}`)

// dirnameRef starts every command: ${__dirname} is the one path the
// specification fixes to the bundle. It shows a relative command once
// and never says what it is resolved against, and the reference host
// resolves no path.
const dirnameRef = "${__dirname}/"

// validateManifest is the referential checks the manifest alone
// answers, as one function so a test can run them against a broken
// manifest without touching the repository. The launcher's names are
// checkLauncherNames', which reads the script.
//
// It takes the staged list as an argument for the same reason: each
// check is watched failing before it is believed, and a check that could
// only be exercised by breaking the real manifest would not be.
func validateManifest(m manifest, files []staged) []string {
	// programs is what can be run. LICENSE and README.md are packed to be
	// read, and nothing may spawn them.
	programs := map[string]staged{}
	for _, f := range files {
		if f.program() {
			programs[f.path] = f
		}
	}
	commands := map[string]string{"": m.Server.MCPConfig.Command}
	for platform, over := range m.Server.MCPConfig.PlatformOverrides {
		if over.Command != "" {
			commands[platform] = over.Command
		}
	}
	problems, runs := commandProblems(m, programs, commands)
	problems = append(problems, spentProblems(m)...)
	return append(problems, platformProblems(m, files, commands, runs)...)
}

// commandProblems is checks 1 and 2. entry_point names a program that
// is actually packed, and the one the default command runs. Every
// command starts with ${__dirname}/ and names a program the packer
// stages. runs is the program each one spawns; a command that names
// none is reported here and left out, so check 5 does not report it
// again.
func commandProblems(m manifest, programs map[string]staged, commands map[string]string) (
	problems []string, runs map[string]staged) {
	switch _, ok := programs[m.Server.EntryPoint]; {
	case m.Server.EntryPoint == "":
		problems = append(problems, "the manifest declares no entry_point")
	case !ok:
		problems = append(problems, fmt.Sprintf("entry_point %q is not a program the packer stages",
			m.Server.EntryPoint))
	}
	runs = map[string]staged{}
	for _, platform := range slices.Sorted(maps.Keys(commands)) {
		cmd := commands[platform]
		where := "mcp_config.command"
		if platform != "" {
			where = "platform_overrides." + platform + ".command"
		}
		rel, prefixed := strings.CutPrefix(cmd, dirnameRef)
		file, isProgram := programs[rel]
		switch {
		case !prefixed:
			problems = append(problems, fmt.Sprintf("%s is %q; a command starts with %s, the one path the "+
				"specification fixes to the bundle", where, cmd, dirnameRef))
		case !isProgram:
			problems = append(problems, fmt.Sprintf("%s is %q, which is not a program the packer stages", where, cmd))
		default:
			runs[platform] = file
		}
	}
	// And check 1's other half: entry_point is the file the default
	// command runs, so the bundle does not name one file as the server
	// and spawn another. An entry point or a command that names no
	// program is reported above.
	_, entryStaged := programs[m.Server.EntryPoint]
	if file, ok := runs[""]; ok && entryStaged && file.path != m.Server.EntryPoint {
		problems = append(problems, fmt.Sprintf("entry_point is %q, and mcp_config.command runs %q",
			m.Server.EntryPoint, file.path))
	}
	return problems, runs
}

// spentProblems is check 3: every ${user_config.x} an env value or an
// argument spends is a declared key. Arguments included, though
// settingProblems refuses one, so that this check holds on its own.
func spentProblems(m manifest) []string {
	spent := map[string][]string{"mcp_config.args": m.args()}
	for key, value := range m.Server.MCPConfig.Env {
		spent["mcp_config.env."+key] = []string{value}
	}
	for platform, over := range m.Server.MCPConfig.PlatformOverrides {
		spent["platform_overrides."+platform+".args"] = over.Args
		for key, value := range over.Env {
			spent["platform_overrides."+platform+".env."+key] = []string{value}
		}
	}
	var problems []string
	for _, where := range slices.Sorted(maps.Keys(spent)) {
		for _, value := range spent[where] {
			for _, ref := range userConfigRef.FindAllStringSubmatch(value, -1) {
				if _, declared := m.UserConfig[ref[1]]; !declared {
					problems = append(problems, fmt.Sprintf(
						"%s spends ${user_config.%s}, which user_config does not declare", where, ref[1]))
				}
			}
		}
	}
	return problems
}

// platformProblems is checks 4 and 5. Every platform_overrides entry
// names a platform the bundle claims: an override nothing can reach is
// the same defect as a command nobody staged. And every claimed
// platform spawns the file staged FOR it, not merely a staged program.
// Deleting an override passes every check above: the platform then runs
// the default command, which is another platform's binary, and that
// file really is in the bundle. A claimed platform nothing is staged
// for is identityProblems'.
func platformProblems(m manifest, files []staged, commands map[string]string, runs map[string]staged) []string {
	var problems []string
	for _, platform := range slices.Sorted(maps.Keys(m.Server.MCPConfig.PlatformOverrides)) {
		if !slices.Contains(m.Compatibility.Platforms, platform) {
			problems = append(problems, fmt.Sprintf(
				"platform_overrides has %q, which compatibility.platforms does not claim", platform))
		}
	}
	for _, platform := range m.Compatibility.Platforms {
		i := slices.IndexFunc(files, func(f staged) bool { return f.platform == platform })
		if i < 0 {
			continue
		}
		from := ""
		if _, ok := commands[platform]; ok {
			from = platform
		}
		if file, ok := runs[from]; ok && file.path != files[i].path {
			problems = append(problems, fmt.Sprintf(
				"platform %q runs %q, and the file staged for it is %q", platform, file.path, files[i].path))
		}
	}
	return problems
}

// args is mcp_config.args as strings: none when the key is missing, null
// or not a list of strings. settingProblems reports the first two, and
// the schema the third.
func (m manifest) args() []string {
	var args []string
	if err := json.Unmarshal(m.Server.MCPConfig.Args, &args); err != nil {
		return nil
	}
	return args
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

// claudeDesktopFloor is the Claude Desktop version every bundle in the
// family claims.
const claudeDesktopFloor = ">=0.10.0"

// identityProblems holds what the manifest says it is: the server's own
// name, a binary, the family's Claude Desktop floor, a privacy policy,
// and exactly the platforms the packer stages an entry point for, so
// dropping one platform and its override together is refused. The
// manifest specification requires privacy_policies of a server that
// connects to a service processing user data, as this one does to
// Pipedrive, and neither the schema nor `mcpb validate` enforces it.
// validateManifest holds each platform to the file staged for it.
func identityProblems(m manifest, files []staged) []string {
	var problems []string
	if m.Name != server.Name {
		problems = append(problems, fmt.Sprintf("the manifest's name is %q and the server's is %q", m.Name, server.Name))
	}
	if m.Server.Type != "binary" {
		problems = append(problems, fmt.Sprintf("server.type is %q; the bundle carries a binary", m.Server.Type))
	}
	if m.Compatibility.ClaudeDesktop != claudeDesktopFloor {
		problems = append(problems, fmt.Sprintf("compatibility.claude_desktop is %q; the family's bundles claim %q",
			m.Compatibility.ClaudeDesktop, claudeDesktopFloor))
	}
	var entryPoints []string
	for _, f := range files {
		if f.platform != "" {
			entryPoints = append(entryPoints, f.platform)
		}
	}
	slices.Sort(entryPoints)
	if claimed := slices.Sorted(slices.Values(m.Compatibility.Platforms)); !slices.Equal(claimed, entryPoints) {
		problems = append(problems, fmt.Sprintf("compatibility.platforms is %v; the packer stages entry points for %v",
			claimed, entryPoints))
	}
	if len(m.PrivacyPolicies) == 0 {
		problems = append(problems, "privacy_policies is empty; the manifest specification requires one for a "+
			"server that connects to a service processing user data, as this one does to Pipedrive")
	}
	return problems
}

// Why bundleLeftOut leaves a setting out.
const (
	diagnostics = "diagnostics, set by hand while chasing a problem"
	tuning      = "tuning, whose default suits an install"
)

// bundleLeftOut is every variable the server reads that the bundle does
// not ask for, with why.
var bundleLeftOut = map[string]string{
	config.EnvLogLevel:    diagnostics,
	config.EnvLogFormat:   diagnostics,
	config.EnvHTTPTimeout: tuning,
}

// bundleVarsFrom is the settings the bundle asks for at install: every
// variable the server reads, vars, less each one leftOut gives a reason
// for. A setting added later is asked for until the manifest asks for it
// or a reason is written. It also refuses an exclusion naming a variable
// the server does not read.
func bundleVarsFrom(vars []config.Var, leftOut map[string]string) (asked []config.Var, problems []string) {
	read := make(map[string]bool, len(vars))
	for _, v := range vars {
		read[v.Name] = true
		if leftOut[v.Name] == "" {
			asked = append(asked, v)
		}
	}
	for _, name := range slices.Sorted(maps.Keys(leftOut)) {
		if !read[name] {
			problems = append(problems, fmt.Sprintf("bundleLeftOut names %s, which the server does not read", name))
		}
	}
	return asked, problems
}

// bundleKey is the user_config key a setting is asked under: its name
// without the prefix, lowercased, which gives the family's bundles the
// same keys.
func bundleKey(name string) string {
	return strings.ToLower(strings.TrimPrefix(name, config.EnvPrefix))
}

// userConfigOption is the part of a user_config entry these checks read.
// Default stays raw, so an absent default is told apart from a null one,
// and value is it decoded, once, as the manifest is.
type userConfigOption struct {
	Type        string          `json:"type"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Required    bool            `json:"required"`
	Sensitive   bool            `json:"sensitive"`
	Default     json.RawMessage `json:"default"`
	value       any
}

// UnmarshalJSON decodes an entry and its default.
func (o *userConfigOption) UnmarshalJSON(data []byte) error {
	type entry userConfigOption
	if err := json.Unmarshal(data, (*entry)(o)); err != nil {
		return err
	}
	if o.Default == nil {
		return nil
	}
	return json.Unmarshal(o.Default, &o.value)
}

// turnedTo is the value a switch takes when the person changes it from
// its default: off for one that defaults to true, and on for any other,
// a missing or unreadable default included.
func (o userConfigOption) turnedTo() bool {
	on, _ := o.value.(bool)
	return !on
}

// turnedNames is keys as a set gives them, each with the value turnedTo
// gives it: [dry_run on].
func turnedNames(m manifest, keys []string) string {
	named := make([]string, len(keys))
	for i, key := range keys {
		named[i] = key + " off"
		if m.UserConfig[key].turnedTo() {
			named[i] = key + " on"
		}
	}
	return "[" + strings.Join(named, ", ") + "]"
}

// settingProblems holds what the bundle asks for to the server's own
// settings, and reports everything wrong rather than the first thing.
//
// Every setting bundleVarsFrom derives is asked for and reaches its
// variable, and nothing else is. A switch is asked as a boolean, so
// Claude Desktop shows it as one, and a credential is marked sensitive.
// An optional key has a default, because the reference host passes
// ${user_config.x} to the server as written when it has none and the
// person leaves the field blank. A default is one the defaults check can
// see: on no required key, on no key whose variable config.Load does not
// read, and holding no ${ for the host to expand. The environment the
// defaults give the server configures it as no environment does: a
// bundle install may turn on nothing an unconfigured server leaves off.
// The sets of switches the server refuses together are the ones
// bundleRefused declares. mcp_config.args is there and empty, and a
// platform override carries neither env nor args, an empty one included:
// the reference host replaces the base's with an override's rather than
// merging, and an argument would configure the server past every check
// on env. leftOut is bundleLeftOut, or a test's.
func settingProblems(m manifest, leftOut map[string]string) []string {
	asked, problems := bundleVarsFrom(config.Vars(), leftOut)
	problems = append(problems, argsProblems(m)...)
	for _, key := range slices.Sorted(maps.Keys(m.UserConfig)) {
		if opt := m.UserConfig[key]; !opt.Required && opt.Default == nil {
			problems = append(problems, fmt.Sprintf("user_config.%s is optional with no default; the reference host "+
				"passes ${user_config.%s} to the server as written when the field is left blank", key, key))
		}
	}
	problems = append(problems, unseenDefaultProblems(m, asked)...)
	switches, asking := askedProblems(m, asked, leftOut)
	problems = append(problems, asking...)
	defaults, defaultsErr := defaultsProblems(m)
	problems = append(problems, defaults...)
	return append(problems, refusalProblems(m, switches, bundleRefused, defaultsErr, startEnv)...)
}

// argsProblems holds mcp_config.args to [] and every platform override
// to no env and no args.
func argsProblems(m manifest) []string {
	var problems []string
	const keepArgs = "keep it as [], since no Claude Desktop launch has shown the key optional"
	switch raw, args := m.Server.MCPConfig.Args, m.args(); {
	case raw == nil:
		problems = append(problems, "mcp_config.args is missing; "+keepArgs)
	case string(raw) == "null":
		problems = append(problems, "mcp_config.args is null; "+keepArgs)
	case len(args) > 0:
		problems = append(problems, fmt.Sprintf("mcp_config.args is %q; the bundle passes every setting "+
			"through mcp_config.env, and an argument would configure the server past these checks", args))
	}
	for _, platform := range slices.Sorted(maps.Keys(m.Server.MCPConfig.PlatformOverrides)) {
		over := m.Server.MCPConfig.PlatformOverrides[platform]
		if over.Env != nil {
			problems = append(problems, fmt.Sprintf("platform_overrides.%s.env is set; a host replaces "+
				"mcp_config.env with it, an empty one too, so put every variable in mcp_config.env", platform))
		}
		if over.Args != nil {
			problems = append(problems, fmt.Sprintf("platform_overrides.%s.args is set; a host replaces "+
				"mcp_config.args with it, an empty one too, and the bundle passes every setting through "+
				"mcp_config.env", platform))
		}
	}
	return problems
}

// askedProblems holds user_config and mcp_config.env to the settings
// asked, both ways: each asked setting has its key, reaches its variable
// under that key, is a boolean if it is a switch, and is sensitive if it
// is a secret; every key is a setting's; and every variable env sets is
// asked for, rather than left out or not read at all. A bundle asking
// for no secret is refused too, since the sensitive check would then
// read nothing. switches is the key of every switch asked for, sorted.
func askedProblems(m manifest, asked []config.Var, leftOut map[string]string) (switches, problems []string) {
	askedName, askedKey := map[string]bool{}, map[string]bool{}
	for _, v := range asked {
		key := bundleKey(v.Name)
		askedName[v.Name], askedKey[key] = true, true
		opt, ok := m.UserConfig[key]
		if !ok {
			problems = append(problems, fmt.Sprintf("user_config does not ask for %s, which sets %s", key, v.Name))
			continue
		}
		if got, want := m.Server.MCPConfig.Env[v.Name], "${user_config."+key+"}"; got != want {
			problems = append(problems, fmt.Sprintf("mcp_config.env.%s is %q, want %q", v.Name, got, want))
		}
		if v.Switch {
			switches = append(switches, key)
			if opt.Type != "boolean" {
				problems = append(problems, fmt.Sprintf("user_config.%s is a %s; %s is a switch, so ask for a boolean",
					key, cmp.Or(opt.Type, "field with no type"), v.Name))
			}
		}
		if v.Secret && !opt.Sensitive {
			problems = append(problems, fmt.Sprintf("user_config.%s is not sensitive; %s is a credential, so mark "+
				"it \"sensitive\": true and Claude Desktop stores it as one", key, v.Name))
		}
	}
	if !slices.ContainsFunc(asked, func(v config.Var) bool { return v.Secret }) {
		problems = append(problems, "the bundle asks for no variable config.Vars marks secret, so no field is held "+
			"to sensitive; the API token is one")
	}
	for _, key := range slices.Sorted(maps.Keys(m.UserConfig)) {
		if !askedKey[key] {
			problems = append(problems, fmt.Sprintf("user_config asks for %s, which is no setting's key", key))
		}
	}
	for _, name := range slices.Sorted(maps.Keys(m.Server.MCPConfig.Env)) {
		switch {
		case askedName[name]:
		case leftOut[name] != "":
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, which the bundle leaves out: %s",
				name, leftOut[name]))
		default:
			problems = append(problems, fmt.Sprintf("mcp_config.env sets %s, which the server does not read", name))
		}
	}
	slices.Sort(switches)
	return switches, problems
}

// hostEnv is the environment the reference host builds from the
// bundle's defaults, with each key in turned set to the value turnedTo
// gives it, as the person changing that field would. A boolean becomes
// "true" or "false", a "" default stays "", a number is written out,
// and a reference with no default is passed through as written.
//
// A variable spending a required key with no default is left out, since
// the person supplies it and it is theirs either way: the API token and
// the company domain. A default that is a list or an object is not
// guessed at: its variable is left out, and its key is in unreadable
// with what it is.
//
// What this builds is checked only as far as config.Load reads it, and
// Load reads neither the token nor the domain. So a default on either,
// or on any required key, is not checked here: unseenDefaultProblems
// refuses it.
//
// It reads mcp_config.env alone. That is sound only because
// settingProblems refuses an argument and an override's env; relaxing
// either needs this to read the same.
func hostEnv(m manifest, turned ...string) (env, unreadable map[string]string) {
	env, unreadable = map[string]string{}, map[string]string{}
	for name, value := range m.Server.MCPConfig.Env {
		var b strings.Builder
		skip, last := false, 0
		for _, at := range userConfigRef.FindAllStringSubmatchIndex(value, -1) {
			key, sub := value[at[2]:at[3]], value[at[0]:at[1]]
			opt := m.UserConfig[key]
			def := opt.value
			if slices.Contains(turned, key) {
				def = opt.turnedTo()
			}
			switch d := def.(type) {
			case bool:
				sub = strconv.FormatBool(d)
			case string:
				sub = d
			case float64:
				sub = strconv.FormatFloat(d, 'f', -1, 64)
			case nil:
				skip = skip || opt.Required
			case []any:
				unreadable[key], skip = "a list", true
			case map[string]any:
				unreadable[key], skip = "an object", true
			}
			b.WriteString(value[last:at[0]] + sub)
			last = at[1]
		}
		if !skip {
			env[name] = b.String() + value[last:]
		}
	}
	return env, unreadable
}

// unseenDefaultProblems refuses every default the defaults check cannot
// see. A required key takes none: the person gives it, and Claude
// Desktop's form may fill one in that they accept unread. A key whose
// variable config.Load does not read takes none either, since starting
// Load from the defaults says nothing about it. And no default holds a
// ${: the host expands ${HOME}, ${DOCUMENTS}, ${DESKTOP},
// ${DOWNLOADS}, ${__dirname} and ${pathSeparator} in a default as well
// as ${user_config.x}, where the defaults check substitutes only the
// last, and an env value is exactly ${user_config.<key>}, so a default
// has no use for one.
func unseenDefaultProblems(m manifest, asked []config.Var) []string {
	loaded := loadedVars()
	var problems []string
	for _, v := range asked {
		key := bundleKey(v.Name)
		opt, ok := m.UserConfig[key]
		switch {
		case !ok || opt.Default == nil:
		case opt.Required:
			problems = append(problems, fmt.Sprintf("user_config.%s is required and has a default; the person "+
				"gives it, and Claude Desktop's form may fill one in that they accept unread", key))
		case !loaded[v.Name]:
			problems = append(problems, fmt.Sprintf("user_config.%s has a default, and config.Load does not read %s, "+
				"so the defaults check cannot see what it does; ask for it with none", key, v.Name))
		}
	}
	for _, key := range slices.Sorted(maps.Keys(m.UserConfig)) {
		if holdsReference(m.UserConfig[key].value) {
			problems = append(problems, fmt.Sprintf("user_config.%s defaults to a value holding ${, which the host "+
				"expands and the defaults check does not", key))
		}
	}
	return problems
}

// holdsReference reports whether a decoded default holds ${ in a string
// anywhere inside it.
func holdsReference(value any) bool {
	switch v := value.(type) {
	case string:
		return strings.Contains(v, "${")
	case []any:
		return slices.ContainsFunc(v, holdsReference)
	case map[string]any:
		for _, inner := range v {
			if holdsReference(inner) {
				return true
			}
		}
	}
	return false
}

// loadedVars is every variable config.Load reads, seen by starting it.
func loadedVars() map[string]bool {
	read := map[string]bool{}
	_, _ = config.Load(gateDomain, func(name string) string {
		read[name] = true
		return ""
	})
	return read
}

// gateDomain is the workspace the defaults check starts the server for.
// The company domain is a required field with no default, so the person
// gives it either way, and the server loads for a domain resolved before
// it reads anything else; one domain serves every start.
const gateDomain = "example"

// loadEnv starts the server's configuration from an environment alone.
func loadEnv(env map[string]string) (config.Config, error) {
	return config.Load(gateDomain, func(name string) string { return env[name] })
}

// startEnv is whether the server starts from an environment alone, which
// is what refusalProblems asks of each set of the switches.
func startEnv(env map[string]string) error {
	_, err := loadEnv(env)
	return err
}

// defaultsProblems reports a bundle whose defaults configure the server
// otherwise than no environment does, naming the fields that differ. A
// default that is a list or an object is reported rather than guessed
// at, every one in key order, and the comparison still runs over the
// rest. defaultsErr is what the server refuses the defaults with, which
// decides whether refusalProblems starts the sets of switches.
func defaultsProblems(m manifest) (problems []string, defaultsErr error) {
	env, unreadable := hostEnv(m)
	for _, key := range slices.Sorted(maps.Keys(unreadable)) {
		problems = append(problems, fmt.Sprintf("user_config.%s defaults to %s, which the defaults check cannot "+
			"build an environment from: give it a boolean, a string or a number", key, unreadable[key]))
	}
	got, defaultsErr := loadEnv(env)
	if defaultsErr != nil {
		problems = append(problems, fmt.Sprintf("the environment the bundle's defaults give the server is refused: %v",
			defaultsErr))
		return problems, defaultsErr
	}
	want, err := loadEnv(nil)
	if err != nil {
		return append(problems, fmt.Sprintf("an unconfigured server is refused: %v", err)), nil
	}
	if diff := fieldDiff(got, want); len(diff) > 0 {
		problems = append(problems, fmt.Sprintf("the bundle's defaults configure the server otherwise than no "+
			"environment does, in %s; a bundle install may turn on nothing an unconfigured server leaves off",
			strings.Join(diff, ", ")))
	}
	return problems, nil
}

// fieldDiff names the fields of two values of one struct type that
// differ. An unexported field cannot be read out through Interface, so
// it is compared with Value.Equal where its type compares by value; one
// that does not is compared as reflect.DeepEqual would, and reported
// unnamed whenever it differs, beside any named field that differs too.
func fieldDiff(a, b any) []string {
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	var fields []string
	unnamed := false
	for i := range va.NumField() {
		f := va.Type().Field(i)
		switch {
		case f.IsExported():
			if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
				fields = append(fields, f.Name)
			}
		case strictlyComparable(f.Type):
			if !va.Field(i).Equal(vb.Field(i)) {
				fields = append(fields, f.Name)
			}
		case !deepEqual(va.Field(i), vb.Field(i)):
			unnamed = true
		}
	}
	if unnamed {
		fields = append(fields, "an unexported field")
	}
	return fields
}

// deepEqual is reflect.DeepEqual over two values of one type read
// through reflection, which reads an unexported field that Interface
// cannot return: a pointer or an interface is followed, a slice or a map
// is compared element by element, and a func is equal only to a nil one.
// A value that points back into itself is not one this compares; the
// server's configuration holds none.
func deepEqual(x, y reflect.Value) bool {
	switch x.Kind() {
	case reflect.Array:
		return elementsEqual(x, y)
	case reflect.Slice:
		return x.IsNil() == y.IsNil() && x.Len() == y.Len() && elementsEqual(x, y)
	case reflect.Map:
		return mapsEqual(x, y)
	case reflect.Pointer, reflect.Interface:
		if x.IsNil() || y.IsNil() {
			return x.IsNil() == y.IsNil()
		}
		if x.Elem().Type() != y.Elem().Type() {
			return false
		}
		return deepEqual(x.Elem(), y.Elem())
	case reflect.Struct:
		for i := range x.NumField() {
			if !deepEqual(x.Field(i), y.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Func:
		return x.IsNil() && y.IsNil()
	default:
		// A boolean, a number or a string, which Equal compares by value,
		// or a channel or an unsafe pointer, which it compares by
		// identity, as DeepEqual does.
		return x.Equal(y)
	}
}

// mapsEqual compares two maps of one type, key by key.
func mapsEqual(x, y reflect.Value) bool {
	if x.IsNil() != y.IsNil() || x.Len() != y.Len() {
		return false
	}
	for _, key := range x.MapKeys() {
		if other := y.MapIndex(key); !other.IsValid() || !deepEqual(x.MapIndex(key), other) {
			return false
		}
	}
	return true
}

// elementsEqual compares two arrays or two slices of one length.
func elementsEqual(x, y reflect.Value) bool {
	for i := range x.Len() {
		if !deepEqual(x.Index(i), y.Index(i)) {
			return false
		}
	}
	return true
}

// strictlyComparable reports whether every value of t compares by value
// with Value.Equal. An interface type compares as a type, but Equal
// panics on a dynamic value that does not, so one anywhere inside t
// rules it out. So does a pointer, a channel, an unsafe pointer or a
// func: Equal compares the first three by address, which says nothing
// about the configuration they reach.
func strictlyComparable(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Interface, reflect.Pointer, reflect.Chan, reflect.UnsafePointer, reflect.Func:
		return false
	case reflect.Struct:
		for i := range t.NumField() {
			if !strictlyComparable(t.Field(i).Type) {
				return false
			}
		}
		return true
	case reflect.Array:
		return strictlyComparable(t.Elem())
	}
	return t.Comparable()
}

// bundleRefused are the sets of the bundle's switches the server refuses
// at start, each sorted, each switch in a set turned from its default.
// Each field in one names the others' titles in its description. The
// server refuses none: its one switch is the dry-run floor, and Load
// refuses no value of it beside another. The gate starts every set of
// the switches and holds the refused ones to this list, both ways, so a
// refusal added to Load fails here until it is declared and the fields
// say so.
var bundleRefused [][]string

// refusalProblems starts every set of the bundle's switches over the
// defaults, each switch in it turned from its default, and holds the
// ones the server refuses to declared: a refusal it does not declare,
// which no description then warns of, and a declared one the server
// starts with, each reported for the smallest set that shows it.
// switches is the user_config key of every switch the bundle asks for,
// sorted, and start is whether the server starts from an environment. A
// declared key that is not one of them is refused too, and so is one
// with no title, which the others' descriptions would name, each once.
// When the defaults alone do not start, which defaultsErr says, it
// starts no set: the defaults check reports that, and every set would
// fail with it.
func refusalProblems(m manifest, switches []string, declared [][]string, defaultsErr error,
	start func(map[string]string) error) []string {
	problems := declaredProblems(m, switches, declared)
	if defaultsErr != nil {
		return problems
	}
	return append(problems, startProblems(m, switches, declared, start)...)
}

// declaredProblems holds each key in a declared set to a switch with a
// title, each once, and each switch's description to the titles of the
// others in its sets, trimmed.
func declaredProblems(m manifest, switches []string, declared [][]string) []string {
	title := func(key string) string { return strings.TrimSpace(m.UserConfig[key].Title) }
	var problems []string
	inSets := map[string]bool{}
	for _, set := range declared {
		for _, key := range set {
			inSets[key] = true
		}
	}
	titled := map[string]bool{}
	for _, key := range slices.Sorted(maps.Keys(inSets)) {
		switch {
		case !slices.Contains(switches, key):
			problems = append(problems, fmt.Sprintf("bundleRefused names %s, which is not a switch the bundle asks for", key))
		case title(key) == "":
			problems = append(problems, fmt.Sprintf("user_config.%s has no title, which the fields the server "+
				"refuses it with must name", key))
		default:
			titled[key] = true
		}
	}
	for _, set := range declared {
		for _, key := range set {
			if !slices.Contains(switches, key) {
				continue
			}
			description := m.UserConfig[key].Description
			for _, other := range set {
				if other != key && titled[other] && !strings.Contains(description, title(other)) {
					problems = append(problems, fmt.Sprintf("user_config.%s does not name %q, which the server "+
						"refuses it with", key, title(other)))
				}
			}
		}
	}
	return problems
}

// startProblems starts every set of the switches over the defaults,
// each switch in the set turned from its default, and holds the
// server's refusals to declared, both ways. Every subset of a set comes
// before it in this order, so the first set to show a defect is the
// smallest, and a superset is not reported again.
func startProblems(m manifest, switches []string, declared [][]string, start func(map[string]string) error) []string {
	var problems []string
	var undeclared []int
	startsDeclared := map[int]bool{}
	for bits := 1; bits < 1<<len(switches); bits++ {
		var turned []string
		for i, key := range switches {
			if bits&(1<<i) != 0 {
				turned = append(turned, key)
			}
		}
		env, _ := hostEnv(m, turned...)
		err := start(env)
		var within []int
		for i, set := range declared {
			if !slices.ContainsFunc(set, func(key string) bool { return !slices.Contains(turned, key) }) {
				within = append(within, i)
			}
		}
		switch {
		case err != nil && len(within) == 0:
			if slices.ContainsFunc(undeclared, func(shown int) bool { return shown&bits == shown }) {
				continue
			}
			undeclared = append(undeclared, bits)
			problems = append(problems, fmt.Sprintf("the server refuses %s, and bundleRefused does not "+
				"declare it, so no field's description warns of it: %v", turnedNames(m, turned), err))
		case err == nil:
			for _, i := range within {
				if !startsDeclared[i] {
					startsDeclared[i] = true
					problems = append(problems, fmt.Sprintf("bundleRefused declares that the server refuses %s, "+
						"and it starts with them", turnedNames(m, declared[i])))
				}
			}
		}
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
// copy, under root, of the schema its own manifest_version names.
func checkAgainstSchema(root string, document map[string]any) error {
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
	return validateAgainstVendored(document, filepath.Join(root, vendoredSchemaPath(version)), want, schemaFor(version))
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

// licenseOf reads the SPDX identifier the LICENSE file under root
// carries, so the manifest cannot claim a different one; "" for a
// license it does not recognize.
//
// It is a referential check like the others: "MIT" in a manifest beside
// an Apache license is well formed, packs, installs, and is wrong about
// the one thing a redistributor reads it for. The first draft of this
// manifest said MIT.
func licenseOf(root string) (string, error) {
	data, err := os.ReadFile(filepath.Join(root, "LICENSE")) //nolint:gosec // a repository path from a constant
	if err != nil {
		return "", fmt.Errorf("cannot read LICENSE: %w", err)
	}
	head := string(data)
	switch {
	case strings.Contains(head, "Apache License"):
		return "Apache-2.0", nil
	case strings.Contains(head, "MIT License"):
		return "MIT", nil
	default:
		return "", nil
	}
}

// The two entry points, in this repository's command signature.

func mcpbCmd(_ io.Writer, _ []string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	return mcpbGate(root)
}

func mcpbPackCmd(_ io.Writer, args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: gates mcpb-pack DIST VERSION OUT")
	}
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	return packMCPB(root, args[0], args[1], args[2])
}
