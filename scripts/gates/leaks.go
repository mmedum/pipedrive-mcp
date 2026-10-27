package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// leaks is the confidentiality gate: nothing that identifies a person, a
// document or a deployment may be in this repository.
//
// It lived in internal/leakcheck as a test. The rules are unchanged; it
// is a command now because the Makefile's target list is what a person
// reads to find out what is checked, and a gate running invisibly inside
// `go test ./...` is one nobody can audit without grepping for it.

var skipFiles = map[string]bool{"go.sum": true}

func leaks(w io.Writer, args []string) error {
	if len(args) == 1 && args[0] == "history" {
		return leakHistory(w)
	}
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
		"developers": true, "app": true, "api": true, "www": true, "companyname": true, "devcommunity": true,
	}

	// A Pipedrive API token is 40 hex characters. A bare 40-hex run is
	// also what a git SHA looks like, so this only fires where the line
	// also names a token — which is where a real one would be pasted.
	apiToken = regexp.MustCompile(`(?i)(token|secret|api[_-]?key)\W{0,4}\b[0-9a-f]{40}\b`)

	// An international phone number, written with + or 00, reaches a
	// person or a desk. Fixtures use the range set aside for fiction,
	// 555-0100 to 555-0199, as the number's last digits. A national number
	// with neither prefix is not caught: too much else has its shape.
	phone          = regexp.MustCompile(`(?:^|[^\w+])((?:\+|00)[1-9]\d{0,2}(?:[ .-]?\(?\d{1,4}\)?){2,5})`)
	fictionalPhone = regexp.MustCompile(`555[ .-]?01\d{2}$`)

	// What an invented value looks like: a marker word, or the run of
	// one character nobody's real data has.
	inventedWord = regexp.MustCompile(`(?i)synthetic|fixture|nosuch|unknown|example|scratch|placeholder|smoke|dummy|sample|acme|xxx|yourcompany|mycompany`)
)

// addressRule names what the address check reports; knownBlobs pins by
// it.
const addressRule = "address at a real domain"

// publicAddresses are addresses at a real domain that name nobody. Each
// is allowed as the whole address, never by its domain, so anything else
// at the same domain is still a finding.
var publicAddresses = map[string]string{
	"noreply@anthropic.com": "the Co-Authored-By trailer on commits Claude helped write: Anthropic's no-reply address",
	"support@github.com":    "the Signed-off-by trailer Dependabot puts on its version bumps: GitHub's support address",
}

// findLeaks returns what is wrong with one file's text.
func findLeaks(text string) []string {
	var found []string
	for _, m := range email.FindAllStringSubmatch(text, -1) {
		if reservedHost.MatchString(m[1]) || invented(m[0]) || publicAddresses[m[0]] != "" {
			continue
		}
		found = append(found, addressRule+": "+m[0])
	}
	for _, m := range companyDomain.FindAllStringSubmatch(text, -1) {
		if invented(m[0]) || invented(m[1]) || vendorHost[strings.ToLower(m[1])] {
			continue
		}
		found = append(found, "a company's Pipedrive subdomain: "+m[0])
	}
	for _, m := range phone.FindAllStringSubmatch(text, -1) {
		if digits(m[1]) < 9 || fictionalPhone.MatchString(m[1]) || invented(m[1]) {
			continue
		}
		found = append(found, "a phone number outside 555-0100..0199: "+m[1])
	}
	for _, m := range apiToken.FindAllString(text, -1) {
		if invented(m) {
			continue
		}
		found = append(found, "what looks like an API token: "+m)
	}
	return found
}

// digits counts the digits in s. A phone number with its country code
// has at least nine; a sum, a version or a diff count has fewer.
func digits(s string) int {
	n := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			n++
		}
	}
	return n
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

// leakHistory is the gate run over everything the repository has
// published rather than what it holds now: an identifier committed and
// edited out later is still on GitHub. It reads the whole object store,
// so it is run by hand, `make leaks-history`, not in `make check`.
func leakHistory(w io.Writer) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	problems, commits, blobs, err := historyProblems(root)
	if err != nil {
		return err
	}
	// 327 commit messages and 1,166 text blobs on 2026-10-25.
	if commits < 100 || blobs < 1000 {
		return fmt.Errorf("only %d commit messages and %d text blobs; the history scan is not reading the "+
			"repository", commits, blobs)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "\n"))
	}
	_, err = fmt.Fprintf(w, "leaks history ok: %d commit messages and %d text blobs scanned\n", commits, blobs)
	return err
}

// knownBlob is a file version in the history that holds a finding the
// maintainer decided to leave rather than rewrite a public history with
// released tags on it.
type knownBlob struct {
	rule   string // the rule that reports it, as findLeaks names it
	count  int    // how many of that rule's findings the blob carries
	reason string
}

// knownBlobs are accepted by blob, never by value. A blob's hash covers
// its content, so a pin cannot grow: the same value in any other blob, in
// a commit message or in the tree is still a finding. The values are not
// written here, since writing one down would publish it again. Decided
// 2026-10-25, as for the other servers' histories.
var knownBlobs = map[string]knownBlob{
	"1e2a523367a979eef1d9ccf3e0597cbf833e3913": {addressRule, 1, "CODE_OF_CONDUCT.md, an earlier version: the maintainer's contact address"},
	"7dff083fd7e9136f27b5a4b6a04d45bd9da19c81": {addressRule, 1, "CONTRIBUTING.md, an earlier version: the maintainer's contact address"},
	"8597bb633a309842ee010fea161f2eb94f0184a7": {addressRule, 1, "CONTRIBUTING.md, an earlier version: the maintainer's contact address"},
	"df376fa25022c02df54002dffddc9050f3dadc9b": {addressRule, 1, "SECURITY.md, an earlier version: the maintainer's contact address"},
	"e8c0062622e1dbe101f9ec688e892faab7e03f98": {addressRule, 1, "SECURITY.md, an earlier version: the maintainer's contact address"},
	"f597a8d3850387945dda1331eb121724563adc03": {addressRule, 1, "SECURITY.md, an earlier version: the maintainer's contact address"},
	"0b34a26a65613b2e1be5d6ad21cb159976e5d136": {addressRule, 2, "an early search test's fixture address, at a real company's domain"},
}

// excused is found less the findings knownBlobs accepts for the blob
// hash.
func excused(hash string, found []string) []string {
	k, ok := knownBlobs[hash]
	if !ok {
		return found
	}
	n := 0
	kept := found[:0]
	for _, f := range found {
		if n < k.count && strings.HasPrefix(f, k.rule+": ") {
			n++
			continue
		}
		kept = append(kept, f)
	}
	return kept
}

// historyProblems is every finding in the history of the repository at
// root, with how many commit messages and text blobs it read: each commit
// message on its own, every path and every blob reachable from any ref,
// and each annotated tag's message. A tag's header is git's, the object
// tagged and the tagger's identity, as a commit's author line is, and
// neither is scanned. A binary blob is skipped: no rule reads one, and
// the tree scan refuses a new one.
func historyProblems(root string) (problems []string, commits, blobs int, err error) {
	problems, commits, err = messageProblems(root)
	if err != nil {
		return nil, 0, 0, err
	}
	hashes, paths, pathOf, err := historyObjects(root)
	if err != nil {
		return nil, 0, 0, err
	}
	for _, found := range findLeaks(allowed(strings.Join(paths, "\n"))) {
		problems = append(problems, "a path in the history: "+found)
	}
	found, blobs, err := objectProblems(root, hashes, pathOf)
	if err != nil {
		return nil, 0, 0, err
	}
	return append(problems, found...), commits, blobs, nil
}

// messageProblems is every finding in root's commit messages, each read
// on its own, with how many it read.
func messageProblems(root string) (problems []string, commits int, err error) {
	messages, err := exec.Command("git", "-C", root, "log", "--all", "-z", "--format=%H%n%B").Output()
	if err != nil {
		return nil, 0, fmt.Errorf("git log: %w", err)
	}
	for entry := range strings.SplitSeq(strings.TrimSuffix(string(messages), "\x00"), "\x00") {
		hash, message, _ := strings.Cut(entry, "\n")
		commits++
		for _, found := range findLeaks(allowed(message)) {
			problems = append(problems, "commit "+short(hash)+" message: "+found)
		}
	}
	return problems, commits, nil
}

// historyObjects is every object reachable from any ref in root, in the
// order git lists them, every path among them, and each object's path or
// tag name.
func historyObjects(root string) (hashes, paths []string, pathOf map[string]string, err error) {
	listing, err := exec.Command("git", "-C", root, "rev-list", "--objects", "--all").Output()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("git rev-list: %w", err)
	}
	pathOf = map[string]string{}
	for line := range strings.Lines(string(listing)) {
		hash, path, _ := strings.Cut(strings.TrimSpace(line), " ")
		hashes = append(hashes, hash)
		if path != "" {
			pathOf[hash] = path
			paths = append(paths, path)
		}
	}
	return hashes, paths, pathOf, nil
}

// objectProblems is every finding in the blobs and tags among hashes, read
// through one git cat-file --batch, with how many text blobs it read.
func objectProblems(root string, hashes []string, pathOf map[string]string) (problems []string, blobs int, err error) {
	cmd := exec.Command("git", "-C", root, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(hashes, "\n") + "\n")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, 0, err
	}
	if err := cmd.Start(); err != nil {
		return nil, 0, fmt.Errorf("git cat-file: %w", err)
	}
	r := bufio.NewReader(stdout)
	for range hashes {
		obj, data, err := nextObject(r)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, 0, err
		}
		name, text, ok := scannable(obj, data, pathOf[obj.hash])
		if !ok {
			continue
		}
		if obj.typ == "blob" {
			blobs++
		}
		for _, found := range excused(obj.hash, findLeaks(allowed(text))) {
			problems = append(problems, name+": "+found)
		}
	}
	if err := cmd.Wait(); err != nil {
		return nil, 0, fmt.Errorf("git cat-file: %w", err)
	}
	return problems, blobs, nil
}

// scannable is what of one object the history scan reads, and the name it
// reports it under: a text blob whole, and a tag's message without its
// header. A binary blob, a commit, read by messageProblems, and a tree,
// whose names are paths, are not read.
func scannable(obj object, data []byte, path string) (name, text string, ok bool) {
	switch obj.typ {
	case "blob":
		if bytes.IndexByte(data, 0) >= 0 {
			return "", "", false
		}
		return path + " (blob " + short(obj.hash) + ")", string(data), true
	case "tag":
		_, message, _ := strings.Cut(string(data), "\n\n")
		return "tag " + path, message, true
	}
	return "", "", false
}

// object is one header line of git cat-file --batch.
type object struct{ hash, typ string }

// nextObject reads one object from git cat-file --batch: its header and
// its content, and the newline after the content.
func nextObject(r *bufio.Reader) (object, []byte, error) {
	header, err := r.ReadString('\n')
	if err != nil {
		return object{}, nil, fmt.Errorf("git cat-file: %w", err)
	}
	fields := strings.Fields(header)
	if len(fields) != 3 {
		return object{}, nil, fmt.Errorf("git cat-file answered %q", strings.TrimSpace(header))
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return object{}, nil, fmt.Errorf("git cat-file answered %q", strings.TrimSpace(header))
	}
	data := make([]byte, size+1)
	if _, err := io.ReadFull(r, data); err != nil {
		return object{}, nil, fmt.Errorf("git cat-file: %w", err)
	}
	return object{hash: fields[0], typ: fields[1]}, data[:size], nil
}

// short is a hash's first eight characters.
func short(hash string) string { return hash[:min(8, len(hash))] }
