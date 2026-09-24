//go:build live

// Command evals drives a model through this server's tools alone and
// scores what it did.
//
// The live integration suite proves the tools work. This proves they
// can be USED, which is a different claim and the one that fails
// quietly: what no driver catches is a result that is internally
// consistent and wrong, and the way to catch it is to give the work to
// something that does not know how the server is built and watch which
// way it goes.
//
// So every task is scored twice. The END STATE is read back through
// this server, because a model's account of what it did is the least
// reliable thing in the run. The TRACE is scored because a task can be
// completed by a model that guessed an id and was lucky: guessing an
// org id and being right is a pass by outcome and a failure by every
// rule this server is built on.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type result struct {
	Task string
	// Failed is set when a check the run could make came back wrong. A
	// check the run could not make is Unverified instead, and never a
	// failure: a task that fails for ever teaches nobody anything.
	Failed     bool
	Problems   []string
	Unverified string
	Calls      int
	Cost       float64
}

func main() {
	if err := runMain(); err != nil {
		fmt.Fprintln(os.Stderr, "evals:", err)
		os.Exit(1)
	}
}

func runMain() error {
	bin := flag.String("bin", "./pipedrive-mcp", "the built server binary to drive")
	model := flag.String("model", "claude-sonnet-5", "model to drive")
	budget := flag.Float64("budget", 1.0, "per-task budget in USD")
	only := flag.String("task", "", "run only tasks whose name contains this")
	keep := flag.Bool("keep-fixture", false, "leave the fixture records in place (for debugging a failure)")
	flag.Parse()

	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("the claude CLI is not on PATH, and it is what drives the model: %w", err)
	}
	if _, err := os.Stat(*bin); err != nil {
		return fmt.Errorf("%s is not built; run make build first: %w", *bin, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	sess, closeSess, err := connect(ctx, *bin)
	if err != nil {
		return err
	}
	defer closeSess()
	h := harnessOver(ctx, sess)

	// Everything this run moves is moved after this instant, which is
	// what makes "what did it touch" answerable without counting the
	// workspace.
	since := time.Now().UTC().Add(-time.Second).Format(time.RFC3339)

	fmt.Println("building the fixture through the server's own tools…")
	fixture, err := buildFixture(h)
	if err != nil {
		// Tear down whatever did get made before giving up.
		if left := teardown(h, fixture); len(left) > 0 {
			fmt.Fprintf(os.Stderr, "AND THE PARTIAL FIXTURE COULD NOT BE REMOVED: %s\n", strings.Join(left, "; "))
		}
		return err
	}
	fmt.Printf("fixture: org %d, person %d, deal %d, activity %d, note %d\n\n",
		fixture.OrgID, fixture.PersonID, fixture.DealID, fixture.ActivityID, fixture.NoteID)

	cfg, cleanupCfg, err := writeMCPConfig(*bin)
	if err != nil {
		return err
	}
	defer cleanupCfg()

	var results []result
	for _, task := range Tasks {
		if *only != "" && !strings.Contains(task.Name, *only) {
			continue
		}
		results = append(results, score(ctx, h, task, fixture, cfg, *model, *budget))

		// Put the shared deal back before the next task, so a task
		// scores its own prompt rather than the one before it.
		if left := resetDeal(h, fixture); left != "" {
			fmt.Printf("  RESET FAILED: %s\n", left)
			fmt.Println("  every task after this one is scoring a state it did not expect")
		}
	}

	if *keep {
		fmt.Println("\n-- fixture kept on request; it is yours to remove --")
	} else {
		fmt.Println("\nremoving the fixture…")
		if left := teardown(h, fixture); len(left) > 0 {
			fmt.Fprintf(os.Stderr, "LEFT BEHIND: %s\n", strings.Join(left, "; "))
		}
	}

	// Anything moved since the run began that is not the fixture's own
	// is the model having touched something nobody asked it to.
	created, edited := listTouched(h, since).unaccounted(fixtureIDs(fixture))
	return report(results, created, edited)
}

// writeMCPConfig points the CLI at the binary under test.
func writeMCPConfig(bin string) (string, func(), error) {
	abs, err := filepath.Abs(bin)
	if err != nil {
		return "", nil, err
	}
	cfg := map[string]any{"mcpServers": map[string]any{
		"pipedrive": map[string]any{"command": abs},
	}}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return "", nil, err
	}
	f, err := os.CreateTemp("", "pipedrive-evals-*.json")
	if err != nil {
		return "", nil, err
	}
	if _, err := f.Write(raw); err != nil {
		return "", nil, err
	}
	_ = f.Close()
	return f.Name(), func() { _ = os.Remove(f.Name()) }, nil
}

// score runs one task and applies both halves.
func score(ctx context.Context, h *Harness, t Task, f Fixture, cfg, model string, budget float64) result {
	res := result{Task: t.Name, Unverified: t.Unverified}
	fmt.Printf("== %s\n", t.Name)

	prompt, err := Substitute(t.Prompt, f)
	if err != nil {
		res.Failed = true
		res.Problems = append(res.Problems, err.Error())
		fmt.Printf("  REFUSED: %v\n", err)
		return res
	}

	r := drive(ctx, prompt, cfg, model, budget)
	res.Calls, res.Cost = len(r.Calls), r.Cost
	if r.Err != nil {
		res.Failed = true
		res.Problems = append(res.Problems, "the run did not complete: "+r.Err.Error())
	}

	// Trace. A task completed by luck is a task that teaches nothing.
	used := make([]string, 0, len(r.Calls))
	for _, c := range r.Calls {
		used = append(used, c.Tool)
	}
	for _, want := range t.MustCall {
		if !slices.Contains(used, want) {
			res.Failed = true
			res.Problems = append(res.Problems, fmt.Sprintf("never called %s (called: %s)", want, strings.Join(used, ", ")))
		}
	}
	for _, avoid := range t.MustNotCall {
		if slices.Contains(used, avoid) {
			res.Failed = true
			res.Problems = append(res.Problems, fmt.Sprintf("reached for %s, which is the path this task exists to rule out", avoid))
		}
	}

	// End state, read back through the server rather than believed.
	if t.Check != nil {
		ok, detail := t.Check(h, f)
		if !ok {
			res.Failed = true
			res.Problems = append(res.Problems, detail)
		}
	}

	for _, p := range res.Problems {
		fmt.Printf("  FAIL %s\n", p)
	}
	// The trace, on failure only. Without it a reader cannot tell the
	// two explanations apart — "the guard is broken" and "the model
	// read the refusal and re-sent with overwrite" produce the same end
	// state, and they are the difference between a server defect and
	// the finding this suite exists to make. Args are the fixture's own
	// invented values and ids, so printing them carries nobody's data.
	if res.Failed {
		for i, c := range r.Calls {
			args, _ := json.Marshal(c.Args)
			flag := ""
			if c.IsError {
				flag = " [refused]"
			}
			fmt.Printf("    %d. %s%s %s\n", i+1, c.Tool, flag, clipArgs(string(args)))
		}
	}
	if res.Unverified != "" {
		fmt.Printf("  UNVERIFIED %s\n", res.Unverified)
	}
	if !res.Failed {
		fmt.Printf("  pass (%d calls, $%.3f)\n", res.Calls, res.Cost)
	}
	return res
}

// drive runs the CLI once and parses the stream.
func drive(ctx context.Context, prompt, cfg, model string, budget float64) *run {
	r := &run{}
	args := []string{
		"-p", prompt,
		"--output-format", "stream-json", "--verbose",
		"--mcp-config", cfg,
		// Only this server's tools. An eval that let the model reach
		// for a shell would be scoring the shell.
		"--strict-mcp-config",
		"--allowed-tools", toolPrefix + "*",
		"--disallowed-tools", "Bash,Read,Write,Edit,WebFetch,WebSearch",
		"--model", model,
		"--max-budget-usd", fmt.Sprintf("%.2f", budget),
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		r.Err = err
		return r
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		r.Err = err
		return r
	}
	parse(bufio.NewScanner(out), r)
	if err := cmd.Wait(); err != nil && r.Err == nil {
		r.Err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return r
}

// report prints the tally and decides the exit status.
func report(results []result, created, edited []string) error {
	var failed, unverified int
	var cost float64
	fmt.Println("\n---")
	for _, r := range results {
		cost += r.Cost
		if r.Failed {
			failed++
		}
		if r.Unverified != "" {
			unverified++
		}
	}
	fmt.Printf("%d task(s), %d failed, %d with a half nobody could check, $%.2f\n",
		len(results), failed, unverified, cost)

	// Reported but not fatal: on a live workspace somebody else editing
	// a record during the run moves it too, and failing on that teaches
	// a reader to ignore the alarm.
	if len(edited) > 0 {
		fmt.Println("\nRecords that moved during the run and are not the fixture's:")
		for _, d := range edited {
			fmt.Println("  " + d)
		}
	}
	if len(created) > 0 {
		fmt.Println("\nTHIS RUN CREATED RECORDS IT CANNOT ACCOUNT FOR:")
		for _, d := range created {
			fmt.Println("  " + d)
		}
		fmt.Println("An eval drives a model, not a script: a task can be answered by creating\n" +
			"something nobody asked for. These rows are real and this harness cannot\n" +
			"remove what it did not create.")
		return fmt.Errorf("the run created %d unaccounted record set(s)", len(created))
	}
	if failed > 0 {
		return fmt.Errorf("%d task(s) failed", failed)
	}
	return nil
}

// clipArgs keeps one call's arguments to a line.
func clipArgs(s string) string {
	const limit = 160
	if len(s) > limit {
		return s[:limit] + "…"
	}
	return s
}
