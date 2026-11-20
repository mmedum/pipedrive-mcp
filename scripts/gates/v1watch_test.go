package main

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
)

// fakePipedrive serves a description with paths, extraPath among them,
// and a changelog whose one deprecation post reads post.
func fakePipedrive(extraPath, post string) func(string) (string, error) {
	var spec strings.Builder
	spec.WriteString("openapi: 3.0.1\npaths:\n")
	for i := range 60 {
		fmt.Fprintf(&spec, "  /thing%d:\n    get: {}\n", i)
	}
	if extraPath != "" {
		fmt.Fprintf(&spec, "  '%s':\n    get: {}\n", extraPath)
	}
	return func(url string) (string, error) {
		switch url {
		case v1WatchSpec:
			return spec.String(), nil
		case v1WatchChangelog:
			return `<a href="/changelog/post/introducing-a">a</a><a href="/changelog/post/introducing-b">b</a>
<a href="/changelog/post/deprecation-of-some-v1-endpoints">c</a>`, nil
		case v1WatchChangelog + "/post/deprecation-of-some-v1-endpoints":
			return post, nil
		case v1WatchChangelog + "/post/introducing-a", v1WatchChangelog + "/post/introducing-b":
			return "new things", nil
		}
		return "", errors.New("unexpected fetch of " + url)
	}
}

func TestV1Watch_PassesWhileTheCarveOutHolds(t *testing.T) {
	// v2 has /users/{id}/followers, which replaces nothing here.
	if err := v1Watch(io.Discard, fakePipedrive("/users/{id}/followers", "GET /v1/deals is deprecated")); err != nil {
		t.Errorf("v1Watch = %v; want nil", err)
	}
}

func TestV1Watch_FailsOnEitherSignal(t *testing.T) {
	cases := []struct{ name, path, post, want string }{
		{"v2 gains notes", "/notes", "", "has /notes; read it"},
		{"v2 gains one note", "/notes/{id}", "", "has /notes/{id}"},
		{"v2 gains users", "/users", "", "has /users;"},
		{"a post deprecates v1 notes", "", "GET /v1/notes is deprecated", "names /v1/notes"},
		{"a post deprecates one v1 note", "", "DELETE /v1/notes/{id} goes", "names /v1/notes"},
		{"a post deprecates whoami's path", "", "GET /v1/users/me is deprecated", "names /v1/users/me"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := v1Watch(io.Discard, fakePipedrive(tc.path, tc.post))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("v1Watch = %v; want an error containing %q", err, tc.want)
			}
		})
	}
}

func TestV1Watch_RefusesASpecItCannotRead(t *testing.T) {
	err := v1Watch(io.Discard, func(string) (string, error) { return "not yaml", nil })
	if !errors.As(err, new(couldNotRun)) || !strings.Contains(err.Error(), "not seeing its input") {
		t.Errorf("v1Watch on an unreadable description = %v; want the floor to refuse", err)
	}
}

func TestV1Watch_IgnoresWhatTheToolsDoNotCall(t *testing.T) {
	post := "GET /v1/notes/{id}/comments and /v1/users/{id} are deprecated; /v1/notesExtra too"
	if err := v1Watch(io.Discard, fakePipedrive("/users/{id}/followers", post)); err != nil {
		t.Errorf("v1Watch = %v; want nil for paths no tool here calls", err)
	}
}

func TestV1Watch_AFailedFetchIsNoVerdict(t *testing.T) {
	err := v1Watch(io.Discard, func(string) (string, error) { return "", errors.New("503") })
	if !errors.As(err, new(couldNotRun)) {
		t.Errorf("v1Watch on a failed fetch = %v; want couldNotRun, which exits 2", err)
	}
}

func TestChangelogPosts_ReadsSlugsAsThePageWritesThem(t *testing.T) {
	page := `<a href="/changelog/post/introducing-deal_id-parameter">x</a>{"url":"\/changelog\/post\/x","u":"/changelog/post/deprecation-of-v1\"}`
	got := changelogPosts(page)
	want := []string{"/changelog/post/deprecation-of-v1", "/changelog/post/introducing-deal_id-parameter"}
	if !slices.Equal(got, want) {
		t.Errorf("changelogPosts = %q, want %q", got, want)
	}
}
