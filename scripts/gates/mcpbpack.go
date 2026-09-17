package main

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The packer. It runs at release time, from the universal binary's post
// hook, which is the one point in the pipeline where every binary exists
// and checksums.txt has not been written yet — that is what MAKES it
// possible for the bundle to be in that file, and therefore under the
// signature. It is not what puts it there: goreleaser hashes the
// artifacts it built, so `checksum.extra_files` is what covers a file a
// hook drops into dist/, and `release.extra_files` is what uploads it.
//
// A .mcpb is a deflate zip with the manifest at the root and the
// binaries under server/. The standard library writes one, which is why
// this is Go and not the official Node CLI: `make check` does not get an
// interpreter nobody declared.

// zipTime is stamped on every entry, so the same inputs give a
// byte-identical archive. Unset writes zeroes, which display as the
// impossible 1980-00-00.
var zipTime = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

// packMCPB writes the bundle for one version.
func packMCPB(dist, version, out string) error {
	stamped, err := stampedManifest(version)
	if err != nil {
		return err
	}
	sources, err := stagedSources(dist)
	if err != nil {
		return err
	}
	return writeBundle(out, stamped, sources, generatedFiles())
}

// stampedManifest checks the committed manifest and returns it with the
// real version written in.
//
// The version goes in through a decode and an encode. A sed over JSON is
// how a quote ends up inside a string.
func stampedManifest(version string) ([]byte, error) {
	if version == "" || version == placeholderVersion {
		return nil, fmt.Errorf("pack needs the real version; %q is the committed placeholder", version)
	}
	m, err := readManifest(manifestPath)
	if err != nil {
		return nil, err
	}
	// Refused rather than overwritten: a committed manifest claiming a
	// version means somebody stamped it in the tree, and the next build
	// would ship whatever they left behind.
	if m.Version != placeholderVersion {
		return nil, fmt.Errorf("%s says version %q; the committed manifest must carry the placeholder %q",
			manifestPath, m.Version, placeholderVersion)
	}
	if problems := validateManifest(m, bundleFiles, launcherNames()); len(problems) > 0 {
		sort.Strings(problems)
		return nil, fmt.Errorf("the manifest does not describe the bundle this would pack:\n  %s",
			strings.Join(problems, "\n  "))
	}

	raw, err := os.ReadFile(manifestPath) //nolint:gosec // a repository path from a constant
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	document["version"] = version
	return json.MarshalIndent(document, "", "  ")
}

// stagedSources resolves every staged name to the file it comes from.
func stagedSources(dist string) (map[string]string, error) {
	sources := map[string]string{}
	for _, f := range bundleFiles {
		if f.generated {
			continue // written from the table, not copied from disk
		}
		path, err := sourceFor(dist, f)
		if err != nil {
			return nil, err
		}
		sources[f.path] = path
	}
	return sources, nil
}

// generatedFiles is what the packer writes rather than copies. Keyed the
// same way as stagedSources, so writeBundle treats the two alike.
func generatedFiles() map[string][]byte {
	out := map[string][]byte{}
	for _, f := range bundleFiles {
		if f.generated {
			out[f.path] = []byte(launcherScript())
		}
	}
	return out
}

// writeBundle writes the deflate zip: the manifest at the root and the
// binaries under server/.
func writeBundle(out string, stamped []byte, sources map[string]string, generated map[string][]byte) error {
	if err := os.MkdirAll(filepath.Dir(out), 0o750); err != nil {
		return err
	}
	file, err := os.Create(out) //nolint:gosec // the output path is the caller's
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	zw := zip.NewWriter(file)
	if err := addEntry(zw, "manifest.json", stamped, 0o644); err != nil {
		return err
	}
	// Sorted, so two packs of the same inputs write the same bytes.
	paths := make([]string, 0, len(sources))
	for p := range sources {
		paths = append(paths, p)
	}
	for p := range generated {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if body, ok := generated[p]; ok {
			// 0755: the launcher is the Linux entry point, and a
			// bundle that installs it unrunnable fails at first use.
			if err := addEntry(zw, p, body, 0o755); err != nil {
				return err
			}
			continue
		}
		// 0755 on every staged binary. The packer forces the execute bit
		// on the entry point alone and copies the filesystem mode for the
		// rest, so the Windows binary arrives unrunnable unless it is
		// staged executable.
		//
		// Streamed rather than read whole: the macOS slot is a universal
		// binary, so holding one in memory to deflate it is tens of
		// megabytes for no reason.
		if err := addFile(zw, p, sources[p], 0o755); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return err
	}
	fmt.Printf("  %s: %d files, version stamped\n", out, len(sources)+len(generated)+1)
	return nil
}

func addEntry(zw *zip.Writer, name string, body []byte, mode os.FileMode) error {
	w, err := entryWriter(zw, name, mode)
	if err != nil {
		return err
	}
	_, err = w.Write(body)
	return err
}

// addFile copies one file into the archive without holding it in memory.
func addFile(zw *zip.Writer, name, path string, mode os.FileMode) error {
	w, err := entryWriter(zw, name, mode)
	if err != nil {
		return err
	}
	f, err := os.Open(path) //nolint:gosec // a path this packer resolved itself
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(w, f)
	return err
}

func entryWriter(zw *zip.Writer, name string, mode os.FileMode) (io.Writer, error) {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipTime}
	header.SetMode(mode)
	return zw.CreateHeader(header)
}

// sourceFor finds the file a staged entry comes from.
//
// A glob must match exactly one file and fails loudly on two: the layout
// under dist/ carries a build id and an amd64 variant, and a bundle
// packed from the wrong binary is not something a checksum catches —
// the checksum is of whatever was built.
func sourceFor(dist string, f staged) (string, error) {
	if f.source != "" {
		if _, err := os.Stat(f.source); err != nil {
			return "", fmt.Errorf("%s: %w", f.path, err)
		}
		return f.source, nil
	}
	pattern := f.glob
	if dist != "dist" {
		pattern = filepath.Join(dist, trimDistPrefix(f.glob))
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("%s: nothing matches %s", f.path, pattern)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("%s: %s matches %d files (%v); a bundle packed from the wrong binary "+
			"is not something a checksum catches", f.path, pattern, len(matches), matches)
	}
}

func trimDistPrefix(glob string) string {
	if rest, ok := strings.CutPrefix(glob, "dist/"); ok {
		return rest
	}
	return glob
}
