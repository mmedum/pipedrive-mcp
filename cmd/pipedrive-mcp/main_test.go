package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/config"
)

func TestNewLogger_TextDefault(t *testing.T) {
	logger := newLogger(config.Config{
		LogLevel:  config.LogInfo,
		LogFormat: config.LogText,
	})
	if logger == nil {
		t.Fatal("newLogger returned nil")
	}
}

func TestNewLogger_JSON(t *testing.T) {
	logger := newLogger(config.Config{
		LogLevel:  config.LogDebug,
		LogFormat: config.LogJSON,
	})
	if logger == nil {
		t.Fatal("newLogger returned nil")
	}
}

func TestNewLogger_AllLevels(t *testing.T) {
	for _, level := range []config.LogLevel{config.LogDebug, config.LogInfo, config.LogWarn, config.LogError} {
		logger := newLogger(config.Config{LogLevel: level, LogFormat: config.LogText})
		if logger == nil {
			t.Errorf("newLogger(%q) returned nil", level)
		}
	}
}

func TestPromptToken_NonTTY(t *testing.T) {
	// os.Pipe is not a TTY; promptToken must fall back to line-read mode.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()

	go func() {
		defer w.Close()
		_, _ = w.WriteString("piped-token\n")
	}()

	var prompt bytes.Buffer
	got, err := promptToken(r, &prompt, "token: ")
	if err != nil {
		t.Fatalf("promptToken: %v", err)
	}
	if got != "piped-token" {
		t.Errorf("token = %q, want piped-token", got)
	}
	if !bytes.Contains(prompt.Bytes(), []byte("token: ")) {
		t.Errorf("prompt not written to writer: %q", prompt.String())
	}
}

func TestPromptToken_StripsCR(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	go func() {
		defer w.Close()
		_, _ = w.WriteString("crlf-token\r\n")
	}()
	got, err := promptToken(r, io.Discard, "")
	if err != nil {
		t.Fatalf("promptToken: %v", err)
	}
	if got != "crlf-token" {
		t.Errorf("token = %q, want crlf-token", got)
	}
}

func TestPromptDomain_HappyPath(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	go func() {
		defer w.Close()
		_, _ = w.WriteString("acme\n")
	}()

	var prompt bytes.Buffer
	got, err := promptDomain(r, &prompt)
	if err != nil {
		t.Fatalf("promptDomain: %v", err)
	}
	if got != "acme" {
		t.Errorf("domain = %q, want acme", got)
	}
	if !bytes.Contains(prompt.Bytes(), []byte("subdomain")) {
		t.Errorf("prompt not written or missing 'subdomain': %q", prompt.String())
	}
}

func TestPromptDomain_StripsCR(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	go func() {
		defer w.Close()
		_, _ = w.WriteString("acme\r\n")
	}()
	got, err := promptDomain(r, io.Discard)
	if err != nil {
		t.Fatalf("promptDomain: %v", err)
	}
	if got != "acme" {
		t.Errorf("domain = %q, want acme", got)
	}
}

func TestPromptDomain_RejectsEmpty(t *testing.T) {
	cases := []string{"", "\n", "   \n", "\t\n"}
	for _, in := range cases {
		t.Run(fmt.Sprintf("input=%q", in), func(t *testing.T) {
			r, w, err := os.Pipe()
			if err != nil {
				t.Fatalf("pipe: %v", err)
			}
			defer r.Close()
			go func() {
				defer w.Close()
				_, _ = w.WriteString(in)
			}()
			_, err = promptDomain(r, io.Discard)
			if err == nil {
				t.Errorf("promptDomain(%q): want error, got nil", in)
			}
		})
	}
}

func TestIsCleanShutdown(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, true},
		{"context canceled", context.Canceled, true},
		{"io.EOF", io.EOF, true},
		{"io.ErrUnexpectedEOF", io.ErrUnexpectedEOF, true},
		{"wrapped EOF", fmt.Errorf("transport: %w", io.EOF), true},
		{"server is closing string", errors.New("server is closing: stdin"), true},
		{"EOF substring", errors.New("read failed: EOF received"), true},
		{"genuine error", errors.New("connection refused"), false},
		{"validation error", errors.New("bad input"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCleanShutdown(tc.err); got != tc.want {
				t.Errorf("isCleanShutdown(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// A mistyped subcommand and a flag reach the default path the same way,
// and the leading dash is all that tells them apart.
//
// This drives run() rather than a predicate. A test of an extracted
// predicate passes with the guard deleted from the dispatch entirely,
// which is the fault this shape of main was changed to avoid.
func TestAnUnknownCommandIsReported(t *testing.T) {
	for _, arg := range []string{"statsu", "zzz-bogus", "Status"} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{arg}, &stdout, &stderr); code == 0 {
			t.Errorf("%q exited 0; a typo is indistinguishable from a correct invocation", arg)
		}
		if !strings.Contains(stderr.String(), arg) {
			t.Errorf("%q: stderr does not name the command: %s", arg, stderr.String())
		}
		if stdout.Len() != 0 {
			t.Errorf("%q wrote to stdout, which carries only MCP frames: %s", arg, stdout.String())
		}
	}
}

// The other direction — that a real flag still reaches the server — is
// deliberately not tested here. runServer registers package-level flags,
// so calling it twice panics with "flag redefined", and it opens a live
// connection with whatever credentials are on the machine: the first
// attempt at this test made real API calls. Holding that direction needs
// runServer to take a FlagSet and its own client, which is the deeper
// change this file's comment on run() describes. Until then it is
// checked by hand against the built binary.
