package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// fakePipedrive serves a description with paths and a changelog whose
// one deprecation post carries body.
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
		{"v2 gains notes", "/notes", "", "has /notes, so the /v1/notes tools can move"},
		{"v2 gains the signed-in user", "/users/me", "", "has /users/me"},
		{"a post deprecates v1 notes", "", "GET /v1/notes is deprecated", "names /v1/notes"},
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
	if err == nil || !strings.Contains(err.Error(), "wrong shape") {
		t.Errorf("v1Watch on an unreadable description = %v; want the floor to refuse", err)
	}
}
