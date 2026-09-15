package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// leaks is the confidentiality gate: nothing that identifies a person, a
// document or a deployment may be in this repository.
//
// It lived in internal/leakcheck as a test. The rules are unchanged; it
// is a command now because the Makefile's target list is what a person
// reads to find out what is checked, and a gate running invisibly inside
// `go test ./...` is one nobody can audit without grepping for it.

// leaks is the confidentiality gate: nothing that identifies a person, a
// document or a deployment may be in this repository.
//
// It lived in internal/leakcheck as a test. The rules are unchanged; it
// is a command now because the Makefile's target list is what a person
// reads to find out what is checked, and a gate running invisibly inside
// `go test ./...` is one nobody can audit without grepping for it.

var skipFiles = map[string]bool{"go.sum": true}

func leaks(w io.Writer, _ []string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	// --others --exclude-standard adds files that are not committed yet
	// but would be by the next `git add -A`. Scanning only the index
	// meant a brand-new file was invisible until someone staged it, so
	// `make check` went green on a working tree carrying an address.
	out, err := exec.Command("git", "-C", root, "ls-files", "-z",
		"--cached", "--others", "--exclude-standard").Output()
	if err != nil {
		// Outside a git checkout there is nothing to enumerate and
		// nothing to be wrong: a module-cache copy or a source tarball
		// is not a repository. Any other failure is a broken check, and
		// a broken check must not pass.
		if ee := (*exec.ExitError)(nil); errors.As(err, &ee) &&
			strings.Contains(string(ee.Stderr), "not a git repository") {
			_, printErr := fmt.Fprintln(w, "leaks: not a git checkout; nothing to enumerate")
			return printErr
		}
		return fmt.Errorf("git ls-files: %w", err)
	}
	files := strings.Split(strings.TrimRight(string(out), "\x00"), "\x00")
	if len(files) < 20 {
		return fmt.Errorf("only %d files; the scan is not seeing the repository", len(files))
	}

	var problems []string
	scanned := 0
	for _, name := range files {
		if skipFiles[filepath.Base(name)] {
			continue
		}
		data, readErr := os.ReadFile(filepath.Join(root, name))
		if readErr != nil {
			continue // deleted or unreadable is not this gate's business
		}
		text := string(data)
		if strings.IndexByte(text, 0) >= 0 {
			// Binary, so none of the rules below can read it — which is
			// how an 8 MB `gates` binary carrying 66 absolute paths from
			// a maintainer's machine passed this gate, `make check` and
			// eight green checks. What can still be said about a binary
			// is whether it belongs in a repository of source at all.
			if why := artifact(data); why != "" {
				problems = append(problems, name+": "+why)
			}
			continue
		}
		scanned++
		for _, found := range findLeaks(allowed(text)) {
			problems = append(problems, name+": "+found)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	_, err = fmt.Fprintf(w, "leaks ok: %d files scanned\n", scanned)
	return err
}

var (
	// An address at a domain someone could actually own. RFC 2606 and
	// RFC 6761 reserve the rest for documentation and tests. This server
	// reads a CRM, so addresses are the identifier it handles most.
	email        = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@([A-Za-z0-9.\-]+\.[A-Za-z]{2,})`)
	reservedHost = regexp.MustCompile(`(?i)(^|\.)(example\.(com|org|net)|test|invalid|localhost|local)$`)

	// A company's Pipedrive subdomain names the customer as plainly as
	// an address does: acme.pipedrive.com is Acme. A format verb is not
	// a subdomain — `https://%s.pipedrive.com/` is the code that builds
	// one — so the match must not start mid-token.
	companyDomain = regexp.MustCompile(`(?i)(?:^|[^a-z0-9%.-])([a-z0-9][a-z0-9-]*)\.pipedrive\.com\b`)

	// Pipedrive's own hosts belong to the vendor, not to a customer.
	vendorHost = map[string]bool{
		"developers": true, "app": true, "api": true, "www": true, "companyname": true,
	}

	// A Pipedrive API token is 40 hex characters. A bare 40-hex run is
	// also what a git SHA looks like, so this only fires where the line
	// also names a token — which is where a real one would be pasted.
	apiToken = regexp.MustCompile(`(?i)(token|secret|api[_-]?key)\W{0,4}\b[0-9a-f]{40}\b`)

	// What an invented value looks like: a marker word, or the run of
	// one character nobody's real data has.
	inventedWord = regexp.MustCompile(`(?i)synthetic|fixture|nosuch|unknown|example|scratch|placeholder|smoke|dummy|sample|acme|xxx|yourcompany|mycompany`)
)

// findLeaks returns what is wrong with one file's text.
func findLeaks(text string) []string {
	var found []string
	for _, m := range email.FindAllStringSubmatch(text, -1) {
		if reservedHost.MatchString(m[1]) || invented(m[0]) {
			continue
		}
		found = append(found, "address at a real domain: "+m[0])
	}
	for _, m := range companyDomain.FindAllStringSubmatch(text, -1) {
		if invented(m[0]) || invented(m[1]) || vendorHost[strings.ToLower(m[1])] {
			continue
		}
		found = append(found, "a company's Pipedrive subdomain: "+m[0])
	}
	for _, m := range apiToken.FindAllString(text, -1) {
		if invented(m) {
			continue
		}
		found = append(found, "what looks like an API token: "+m)
	}
	return found
}

// invented reports whether a match is obviously made up, either by
// carrying a marker word or by repeating one character further than real
// data does.
func invented(s string) bool {
	return inventedWord.MatchString(s) || hasRun(s, 6)
}

// hasRun reports whether s repeats one character n times in a row.
func hasRun(s string, n int) bool {
	run := 1
	for i := 1; i < len(s); i++ {
		if s[i] == s[i-1] {
			run++
			if run >= n {
				return true
			}
			continue
		}
		run = 1
	}
	return false
}

// allowed drops the lines that carry the marker. The planted strings in
// this file's own table are the case it exists for: a scanner has to
// contain the shapes it catches, and the marker is per line, so nothing
// else in the file is excused. gitleaks reads its own marker the same
// way and on the same lines, for the same reason.
func allowed(text string) string {
	var b strings.Builder
	for line := range strings.Lines(text) {
		if strings.Contains(line, "leakcheck:"+"allow") {
			continue
		}
		b.WriteString(line)
	}
	return b.String()
}

// Magic numbers of a linked executable. `go build ./scripts/gates`
// drops one beside the source under whatever name the directory has,
// and `git add -A` sweeps it in; two of the four servers in this family
// did exactly that, and one pushed three of them to a public main,
// where they are 97% of the packed history.
var executableMagic = [][]byte{
	[]byte("\x7fELF"),                                  // Linux, BSD
	[]byte("MZ"),                                       // Windows PE
	{0xfe, 0xed, 0xfa, 0xce}, {0xce, 0xfa, 0xed, 0xfe}, // Mach-O, 32-bit
	{0xfe, 0xed, 0xfa, 0xcf}, {0xcf, 0xfa, 0xed, 0xfe}, // Mach-O, 64-bit
	{0xca, 0xfe, 0xba, 0xbe}, // Mach-O universal
}

// maxBinary is what a binary file may weigh before it has to justify
// itself. Nothing binary is tracked in this repository at all, so this
// is a floor on what may be added rather than a description of what is
// here; a fixture that genuinely needs to be binary will be far under
// it, and a build artifact will not.
const maxBinary = 1 << 20

// artifact reports why a binary file does not belong in the repository,
// or "" if it may stay. It is deliberately not overridable by a marker
// comment: a binary cannot carry one, and the way past this rule should
// be an edit somebody reviews.
func artifact(data []byte) string {
	for _, magic := range executableMagic {
		if bytes.HasPrefix(data, magic) {
			return fmt.Sprintf("a compiled executable, %d bytes. Nothing built belongs in the "+
				"tree; add it to .gitignore", len(data))
		}
	}
	if len(data) > maxBinary {
		return fmt.Sprintf("%d bytes of binary, past the %d-byte limit. No rule here can read it, "+
			"so it is carried on trust", len(data), maxBinary)
	}
	return ""
}
