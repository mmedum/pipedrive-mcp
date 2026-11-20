package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The sources v1watch reads. Notes and users come from Pipedrive v1
// because v2 has neither (D-04, D-105); either fact can change between
// releases, and these pages are where Pipedrive says so.
const (
	v1WatchSpec      = "https://developers.pipedrive.com/docs/api/v1/openapi-v2.yaml"
	v1WatchChangelog = "https://developers.pipedrive.com/changelog"
)

// v1WatchCarveOut is each v1 resource a tool calls: the v2 prefix whose
// paths would replace it, and the v1 paths the tools call, matched with
// a boundary so a sub-resource such as /v1/notes/{id}/comments is not one.
var v1WatchCarveOut = []struct {
	v2Prefix string
	v1       *regexp.Regexp
	v1Name   string
}{
	{"/notes", regexp.MustCompile(`/v1/notes(/\{id\})?([^/A-Za-z0-9_{]|$)`), "/v1/notes"},
	{"/users", regexp.MustCompile(`/v1/users/me([^/A-Za-z0-9_]|$)`), "/v1/users/me"},
	{"/leads", regexp.MustCompile(`/v1/leads(/\{id\}|/archived)?([^/A-Za-z0-9_{]|$)`), "/v1/leads"},
	{"/leadLabels", regexp.MustCompile(`/v1/leadLabels([^/A-Za-z0-9_{]|$)`), "/v1/leadLabels"},
	{"/goals", regexp.MustCompile(`/v1/goals(/find|/\{id\}(/results)?)?([^/A-Za-z0-9_{]|$)`), "/v1/goals"},
}

// v1WatchKnownV2 are v2 paths under a carve-out prefix that were read and
// replace nothing here. A new one fails the gate until someone reads it
// and adds it, with the reason.
var v1WatchKnownV2 = map[string]string{
	"/users/{id}/followers":                      "a user's followers, not the signed-in user whoami reads",
	"/leads/search":                              "lead search, beside the itemSearch search calls; no lead records",
	"/leads/{id}/convert/deal":                   "converting a lead to a deal; no lead records",
	"/leads/{id}/convert/status/{conversion_id}": "a conversion's status; no lead records",
}

// v1WatchKnownPosts are changelog posts that name a carve-out path and
// were read and found harmless, with the reason. None yet.
var v1WatchKnownPosts = map[string]string{}

// v1WatchGate checks the carve-out against Pipedrive's own pages: the v2
// description gaining /notes or /users means the v1 tools can move, and
// a deprecation post naming their v1 paths means they are at risk. It
// needs the network, and fails when the network does: a stale answer to
// "is v1 being retired" is a wrong one.
func v1WatchGate(out io.Writer, _ []string) error {
	client := &http.Client{Timeout: 30 * time.Second}
	fetch := func(url string) (string, error) {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
		if err != nil {
			return "", err
		}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("%s answered %d", url, resp.StatusCode)
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
		return string(body), err
	}
	return v1Watch(out, fetch)
}

// v1Watch is the gate with its fetch injected, so a test drives it. A
// fetch that fails or a page it cannot read is no verdict, and exits 2.
func v1Watch(out io.Writer, fetch func(string) (string, error)) error {
	spec, err := fetch(v1WatchSpec)
	if err != nil {
		return couldNotRun{err}
	}
	paths := v2SpecPaths(spec)
	// Floors stated literally: the description has well over 50 paths
	// and the changelog index links about a dozen posts.
	if p := floorProblems("paths in the v2 description", len(paths), 50); p != nil {
		return couldNotRun{fmt.Errorf("v1-watch: %s", p[0])}
	}
	problems := v2Problems(paths)

	index, err := fetch(v1WatchChangelog)
	if err != nil {
		return couldNotRun{err}
	}
	posts := changelogPosts(index)
	if p := floorProblems("posts on the changelog index", len(posts), 3); p != nil {
		return couldNotRun{fmt.Errorf("v1-watch: %s", p[0])}
	}
	postProblems, err := changelogProblems(posts, fetch)
	if err != nil {
		return err
	}
	problems = append(problems, postProblems...)
	if len(problems) > 0 {
		return fmt.Errorf("v1-watch:\n  %s", strings.Join(problems, "\n  "))
	}
	prefixes, v1s := make([]string, 0, len(v1WatchCarveOut)), make([]string, 0, len(v1WatchCarveOut))
	for _, c := range v1WatchCarveOut {
		prefixes, v1s = append(prefixes, c.v2Prefix), append(v1s, c.v1Name)
	}
	_, err = fmt.Fprintf(out, "v1-watch ok: no new v2 path under %s in %d paths, and none of %d changelog posts names %s\n",
		strings.Join(prefixes, ", "), len(paths), len(posts), strings.Join(v1s, ", "))
	return err
}

// v2Problems is each v2 path under a carve-out prefix nobody has read.
func v2Problems(paths []string) []string {
	var problems []string
	for _, c := range v1WatchCarveOut {
		for _, p := range paths {
			if p != c.v2Prefix && !strings.HasPrefix(p, c.v2Prefix+"/") {
				continue
			}
			if _, known := v1WatchKnownV2[p]; !known {
				problems = append(problems, fmt.Sprintf("Pipedrive's v2 description has %s; read it, and move the %s tools off v1 if it replaces them, or add it to v1WatchKnownV2 (D-04)", p, c.v1Name))
			}
		}
	}
	return problems
}

// changelogProblems reads every post and names each that names a path a
// tool calls, unless it was read before.
func changelogProblems(posts []string, fetch func(string) (string, error)) ([]string, error) {
	var problems []string
	for _, post := range posts {
		body, err := fetch(v1WatchChangelog + strings.TrimPrefix(post, "/changelog"))
		if err != nil {
			return nil, couldNotRun{err}
		}
		if _, known := v1WatchKnownPosts[post]; known {
			continue
		}
		for _, c := range v1WatchCarveOut {
			if c.v1.MatchString(body) {
				problems = append(problems, fmt.Sprintf("the changelog post %s names %s, which a tool here calls; read it, and add it to v1WatchKnownPosts if it is harmless (D-04)", post, c.v1Name))
			}
		}
	}
	return problems, nil
}

var (
	v2PathLine = regexp.MustCompile(`(?m)^ {2}'?(/[^':]*)'?:\s*$`)
	// A slug runs to the end of its attribute or JSON string: Pipedrive's
	// carry underscores, as in introducing-deal_id-parameter-…, and the
	// page also holds them escaped inside JSON.
	postLink = regexp.MustCompile(`/changelog/post/[^"'#?\s<>\\]+`)
)

// v2SpecPaths is the path keys of the description's paths: map, which
// sit at two spaces of indent, quoted when they hold braces.
func v2SpecPaths(spec string) []string {
	matches := v2PathLine.FindAllStringSubmatch(spec, -1)
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		out = append(out, m[1])
	}
	return out
}

// changelogPosts is each post the changelog index links, once.
func changelogPosts(index string) []string {
	posts := postLink.FindAllString(index, -1)
	slices.Sort(posts)
	return slices.Compact(posts)
}
