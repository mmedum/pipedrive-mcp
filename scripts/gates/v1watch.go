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
// because v2 has neither (D-03, D-04); either fact can change between
// releases, and these pages are where Pipedrive says so.
const (
	v1WatchSpec      = "https://developers.pipedrive.com/docs/api/v1/openapi-v2.yaml"
	v1WatchChangelog = "https://developers.pipedrive.com/changelog"
)

// v1WatchCarveOut is each v1 path a tool calls, and the v2 path that
// would replace it. /users/me is whoami's: v2's description has
// /users/{id}/followers, which replaces nothing here.
var v1WatchCarveOut = []struct{ v1, v2 string }{
	{"/v1/notes", "/notes"},
	{"/v1/users/me", "/users/me"},
}

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

// v1Watch is the gate with its fetch injected, so a test drives it.
func v1Watch(out io.Writer, fetch func(string) (string, error)) error {
	spec, err := fetch(v1WatchSpec)
	if err != nil {
		return err
	}
	paths := v2SpecPaths(spec)
	// A floor stated literally: the description has well over this many
	// paths, and a parser reading the wrong shape finds none.
	const minPaths = 50
	if len(paths) < minPaths {
		return fmt.Errorf("v1-watch: read %d paths from the v2 description, fewer than %d; it is reading the wrong shape", len(paths), minPaths)
	}
	var problems []string
	for _, c := range v1WatchCarveOut {
		if slices.Contains(paths, c.v2) {
			problems = append(problems, fmt.Sprintf("Pipedrive's v2 description has %s, so the %s tools can move off v1 (D-04)", c.v2, c.v1))
		}
	}

	index, err := fetch(v1WatchChangelog)
	if err != nil {
		return err
	}
	posts := changelogPosts(index)
	const minPosts = 3
	if len(posts) < minPosts {
		return fmt.Errorf("v1-watch: read %d posts from the changelog, fewer than %d; it is reading the wrong shape", len(posts), minPosts)
	}
	read := 0
	for _, post := range posts {
		if !strings.Contains(post, "deprecat") && !strings.Contains(post, "v1") {
			continue
		}
		body, fetchErr := fetch(v1WatchChangelog + strings.TrimPrefix(post, "/changelog"))
		if fetchErr != nil {
			return fetchErr
		}
		read++
		for _, c := range v1WatchCarveOut {
			if strings.Contains(body, c.v1) {
				problems = append(problems, fmt.Sprintf("the changelog post %s names %s, which a tool here calls (D-04)", post, c.v1))
			}
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("v1-watch:\n  %s", strings.Join(problems, "\n  "))
	}
	_, err = fmt.Fprintf(out, "v1-watch ok: v2 has no /notes or /users/me in %d paths, and %d deprecation posts of %d name neither v1 path\n", len(paths), read, len(posts))
	return err
}

var (
	v2PathLine = regexp.MustCompile(`(?m)^ {2}'?(/[^':]*)'?:\s*$`)
	postLink   = regexp.MustCompile(`/changelog/post/[a-z0-9-]+`)
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
