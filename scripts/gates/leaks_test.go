package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Each rule fires on a real-looking value and stays silent on an
// invented one. Real-looking values are built by concatenation, so the
// gate never reads one whole in this file.
func TestFindLeaks(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string // a substring of the one finding, or "" for none
	}{
		{"address at a real domain", "mail " + "kim@" + "vexqorlund-freight.dk", "address at a real domain"},
		{"address at a reserved domain", "mail kim@example.com", ""},
		{"address with a marker word", "mail fixture@" + "vexqorlund-freight.dk", ""},
		{"company subdomain", "see https://" + "vexqorlund" + ".pipedrive.com/deal/1", "Pipedrive subdomain"},
		{"invented subdomain", "see https://acme.pipedrive.com/deal/1", ""},
		{"vendor subdomain", "see https://developers.pipedrive.com/docs", ""},
		{"format verb is not a subdomain", "fmt.Sprintf(`https://%s.pipedrive.com/`, d)", ""},
		{"api token", "api_token=" + strings.Repeat("3f9a", 10), "API token"},
		// Real-looking numbers are from ranges set aside for drama (Ofcom,
		// ACMA), so none of them reaches anyone.
		{"phone number", `"value":"+44 20 ` + `7946 0958"`, "phone number"},
		{"phone number with dashes", "call +61-" + "491-570-156", "phone number"},
		{"phone number with parentheses", "call +44 (20) " + "7946 0321", "phone number"},
		{"phone number with 00", "call 0044 20 " + "7946 0174", "phone number"},
		{"fiction digits inside a longer number", "call +1 212 " + "555 0100 99 88", "phone number"},
		{"fictional phone number", `"value":"+1 555 0100"`, ""},
		{"fictional phone number with area code", "call +1-202-555-0142", ""},
		{"a sum is not a phone number", "attempt+1 < max", ""},
		{"a version is not a phone number", "go 1.26 +1.27 builds", ""},
		{"Dependabot's sign-off", "Signed-off-by: dependabot[bot] <support" + "@github.com>", ""},
		{"another address at github.com", "mail someone" + "@github.com", "address at a real domain"},
		{"an address ending in an allowed one", "mail x.support" + "@github.com", "address at a real domain"},
		{"the forum is Pipedrive's", "see https://devcommunity" + ".pipedrive.com/t/1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := findLeaks(tc.text)
			if tc.want == "" {
				if len(got) != 0 {
					t.Errorf("findLeaks(%q) = %q, want no finding", tc.text, got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], tc.want) {
				t.Errorf("findLeaks(%q) = %q, want one finding naming %q", tc.text, got, tc.want)
			}
		})
	}
}

// A pin excuses as many of its rule's findings as it counts, in its own
// blob, and nothing else.
func TestExcusedHoldsAPinToItsBlobRuleAndCount(t *testing.T) {
	const pinned = "0b34a26a65613b2e1be5d6ad21cb159976e5d136"
	address := addressRule + ": a"
	phone := "a phone number outside 555-0100..0199: 1"
	for _, tc := range []struct {
		name, hash  string
		found, want []string
	}{
		{"the pin's own findings", pinned, []string{address, address}, []string{}},
		{"one more than it counts", pinned, []string{address, address, address}, []string{address}},
		{"another rule in the same blob", pinned, []string{address, phone}, []string{phone}},
		{"another blob", strings.Repeat("0", 40), []string{address}, []string{address}},
	} {
		if got := excused(tc.hash, slices.Clone(tc.found)); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

// The history scan reads a commit message, an old version of a file, a
// path and a tag's message, and leaves a tag's header and a binary alone.
func TestHistoryProblemsReadsWhatWasPublished(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	address := "kim@" + "vexqorlund-freight.dk"
	git := func(email string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "user.name=Fixture", "-c", "user.email=" + email,
			"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false", "-c", "init.defaultBranch=main"}, args...)...)
		cmd.Env = append(os.Environ(), "HOME="+home, "XDG_CONFIG_HOME="+home, "GIT_CONFIG_NOSYSTEM=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	git("fixture@example.com", "init", "-q")
	write("notes.txt", "mail "+address+"\n")
	write("tool.bin", "\x00"+address)
	git("fixture@example.com", "add", ".")
	git("fixture@example.com", "commit", "-q", "-m", "Add notes\n\nsee https://"+"vexqorlund"+".pipedrive.com")
	write("notes.txt", "nothing here\n")
	write("call "+"+44 20 "+"7946 0958.txt", "")
	git("fixture@example.com", "add", ".")
	git("fixture@example.com", "commit", "-q", "-m", "Clean the notes")
	git(address, "tag", "-a", "v0.0.1", "-m", "call +61-"+"491-570-156")

	problems, commits, blobs, err := historyProblems(root)
	if err != nil {
		t.Fatal(err)
	}
	if commits != 2 || blobs != 3 {
		t.Errorf("read %d commit messages and %d text blobs, want 2 and 3", commits, blobs)
	}
	kinds := make([]string, 0, len(problems))
	for _, p := range problems {
		where, what, _ := strings.Cut(p, ": ")
		switch {
		case strings.HasPrefix(where, "commit "):
			where = "commit"
		case strings.HasPrefix(where, "notes.txt (blob "):
			where = "notes.txt"
		}
		kinds = append(kinds, where+" | "+what)
	}
	slices.Sort(kinds)
	want := []string{
		"a path in the history | a phone number outside 555-0100..0199: +44 20 " + "7946 0958",
		"commit | a company's Pipedrive subdomain: /vexqorlund" + ".pipedrive.com",
		"notes.txt | " + addressRule + ": " + address,
		"tag v0.0.1 | a phone number outside 555-0100..0199: +61-" + "491-570-156",
	}
	if !slices.Equal(kinds, want) {
		t.Errorf("found\n%q\nwant\n%q", kinds, want)
	}
}
