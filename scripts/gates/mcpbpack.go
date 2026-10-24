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

// packMCPB writes the bundle for one version, from the repository at
// root and the binaries under dist, to out.
//
// The archive is written beside out under a name no bundle glob
// matches, and renamed to out only once it is whole and closed. A pack
// that fails part way leaves nothing behind, so neither a later run nor
// goreleaser's ./dist/*.mcpb can take a truncated archive for the
// bundle, and an out that was already there is left as it was.
func packMCPB(root, dist, version, out string) error {
	stamped, err := stampedManifest(root, version)
	if err != nil {
		return err
	}
	sources, err := stagedSources(root, dist)
	if err != nil {
		return err
	}
	generated := generatedFiles()
	err = os.MkdirAll(filepath.Dir(out), 0o750)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(out), filepath.Base(out)+".*.partial")
	if err != nil {
		return err
	}
	err = finishBundle(file, file.Name(), out, func(w io.Writer) error {
		return writeBundle(w, stamped, sources, generated)
	})
	if err != nil {
		return err
	}
	fmt.Printf("  %s: %d files, version stamped\n", out, len(sources)+len(generated)+1)
	return nil
}

// finishBundle writes the bundle into file through write, closes it, and
// renames name, where file is, to out. It is the one place the file is
// closed, and Close's error is the pack's: the last of the archive can
// reach the disk only there, and a bundle cut short must not pass. On
// any failure name is removed and out is not touched.
func finishBundle(file io.WriteCloser, name, out string, write func(io.Writer) error) error {
	err := write(file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(name, out)
	}
	if err != nil {
		_ = os.Remove(name)
	}
	return err
}

// stampedManifest is the committed manifest under root with the real
// version written in, as the bundle carries it.
//
// It starts where the gate starts, from the document committedManifest
// decoded, so the release packs no manifest the gate would refuse; the
// placeholder version is among those checks, because a committed
// manifest claiming a version means somebody stamped it in the tree, and
// the next build would ship whatever they left behind. The version goes
// in through the decoded document and an encode, because a sed over JSON
// is how a quote ends up inside a string. The stamped document is held
// to the schema again: what the bundle carries has a version that came
// from a tag rather than from the file somebody reviewed. It is the
// document the bytes encode, since an encode of a decoded document
// decodes back to it.
func stampedManifest(root, version string) ([]byte, error) {
	if version == "" || version == placeholderVersion {
		return nil, fmt.Errorf("pack needs the real version; %q is the committed placeholder", version)
	}
	document, _, problems, err := committedManifest(root)
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		return nil, fmt.Errorf("the manifest does not describe the bundle this would pack:\n  %s",
			strings.Join(problems, "\n  "))
	}
	document["version"] = version
	if err := checkAgainstSchema(root, document); err != nil {
		return nil, fmt.Errorf("the stamped manifest would not satisfy the schema it cites: %w", err)
	}
	return json.MarshalIndent(document, "", "  ")
}

// stagedSources resolves every staged name to the file it comes from:
// a binary under dist, or a file of the repository at root.
func stagedSources(root, dist string) (map[string]string, error) {
	sources := map[string]string{}
	for _, f := range bundleFiles {
		if f.generated {
			continue // written from the table, not copied from disk
		}
		path, err := sourceFor(root, dist, f)
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

// writeBundle writes the deflate zip to file: the manifest at the root,
// the binaries and the launcher under server/, and the license and the
// README beside the manifest. Closing file is finishBundle's.
func writeBundle(file io.Writer, stamped []byte, sources map[string]string, generated map[string][]byte) error {
	modes := map[string]os.FileMode{}
	for _, f := range bundleFiles {
		modes[f.path] = f.mode()
	}
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
		// 0755 on the launcher and every staged binary, 0644 on what is
		// there to be read. The packer forces the execute bit on the
		// entry point alone and copies the filesystem mode for the rest,
		// so the Windows binary arrives unrunnable unless it is staged
		// executable, and a launcher installed unrunnable fails every
		// Linux user at first use.
		if body, ok := generated[p]; ok {
			if err := addEntry(zw, p, body, modes[p]); err != nil {
				return err
			}
			continue
		}
		// Streamed rather than read whole: the macOS slot is a universal
		// binary, so holding one in memory to deflate it is tens of
		// megabytes for no reason.
		if err := addFile(zw, p, sources[p], modes[p]); err != nil {
			return err
		}
	}
	return zw.Close()
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

// sourceFor finds the file a staged entry comes from: a file of the
// repository at root, or a binary under dist.
//
// A glob must match exactly one file and fails loudly on two: the layout
// under dist/ carries a build id and an amd64 variant, and a bundle
// packed from the wrong binary is not something a checksum catches —
// the checksum is of whatever was built.
func sourceFor(root, dist string, f staged) (string, error) {
	if f.source != "" {
		path := filepath.Join(root, f.source)
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("%s: %w", f.path, err)
		}
		return path, nil
	}
	pattern := filepath.Join(dist, trimDistPrefix(f.glob))
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
