package config_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mmedum/pipedrive-mcp/internal/app"
	"github.com/mmedum/pipedrive-mcp/internal/config"
	"github.com/mmedum/pipedrive-mcp/internal/credentials"
)

// The registry of variables, which the bundle gate derives what it asks
// for from. These are external tests so the readers outside Load, in
// internal/app and internal/credentials, can be driven from here.

// varsFloor is how many variables the server reads today, on purpose:
// the floor moves with it, so a registry that records nothing cannot
// pass a walk over it.
const varsFloor = 6

// registry is config.Vars, refused below the floor.
func registry(t *testing.T) []config.Var {
	t.Helper()
	vars := config.Vars()
	if len(vars) < varsFloor {
		t.Fatalf("Vars has %d variables, want at least %d", len(vars), varsFloor)
	}
	return vars
}

// load is config.Load for a fixed domain over env alone.
func load(env map[string]string) (config.Config, error) {
	return config.Load("acme", func(name string) string { return env[name] })
}

// loadReads is every variable Load reads, with how many times.
func loadReads() map[string]int {
	reads := map[string]int{}
	_, _ = config.Load("acme", func(name string) string { reads[name]++; return "" })
	return reads
}

// EnvVars is every variable the server reads, the two read outside Load
// included.
func TestEnvVars(t *testing.T) {
	want := []string{
		"LOG_FORMAT", "LOG_LEVEL", "PIPEDRIVE_API_TOKEN", "PIPEDRIVE_COMPANY_DOMAIN",
		"PIPEDRIVE_DRY_RUN", "PIPEDRIVE_HTTP_TIMEOUT",
	}
	if got := config.EnvVars(); !slices.Equal(got, want) {
		t.Errorf("EnvVars =\n%v\nwant\n%v", got, want)
	}
}

// A variable is marked a switch exactly when Load reads it as a boolean.
func TestVarsDescribeWhatLoadDoes(t *testing.T) {
	var switches []string
	for _, v := range registry(t) {
		accepts := func(value string) bool {
			_, err := load(map[string]string{v.Name: value})
			return err == nil
		}
		if boolean := accepts("true") && accepts("false") && !accepts("maybe"); boolean != v.Switch {
			t.Errorf("%s: Switch is %t, and Load reads it as a boolean: %t", v.Name, v.Switch, boolean)
		}
		if v.Switch {
			switches = append(switches, v.Name)
		}
	}
	if want := []string{"PIPEDRIVE_DRY_RUN"}; !slices.Equal(switches, want) {
		t.Errorf("switches %v, want %v", switches, want)
	}
}

// Load reads each variable once, and records each once for Vars. Two
// settings on one variable would leave it to whichever Load reads last.
func TestLoadReadsEachVariableOnce(t *testing.T) {
	reads := loadReads()
	outside := []string{config.DomainEnv, config.EnvAPIToken}
	var listed []string
	for _, v := range registry(t) {
		if !slices.Contains(outside, v.Name) {
			listed = append(listed, v.Name)
		}
	}
	if read := slices.Sorted(maps.Keys(reads)); !slices.Equal(listed, read) {
		t.Errorf("Vars records %v as Load's\nLoad reads %v", listed, read)
	}
	for name, n := range reads {
		if n != 1 {
			t.Errorf("Load reads %s %d times", name, n)
		}
	}
	// A variable Load reads and something outside it reads too would be
	// read under two rules; EnvVars lists it twice, so this sees it.
	if names := config.EnvVars(); !slices.Equal(names, slices.Compact(slices.Clone(names))) {
		t.Errorf("EnvVars names a variable twice: %v", names)
	}
}

// A variable of spaces alone is unset, as an empty one is, and the spaces
// around a value are dropped, so a form field cleared to a space starts
// the server with the default. That holds for every variable the server
// reads: Load's through Load, and each one read outside Load through its
// reader. One read outside Load with no reader here fails, so a variable
// added there cannot miss the rule.
func TestASettingOfSpacesIsUnset(t *testing.T) {
	outside := map[string]func(t *testing.T, value string) string{
		config.DomainEnv: func(t *testing.T, value string) string {
			// An unset domain falls to the userconfig pointer, so the
			// config directory is an empty one rather than the real one.
			dir := t.TempDir()
			for _, name := range []string{"HOME", "XDG_CONFIG_HOME", "AppData"} {
				t.Setenv(name, dir)
			}
			t.Setenv(config.DomainEnv, value)
			domain, src, err := app.ResolveDomainAtStartup()
			return fmt.Sprintf("%q %q %v", domain, src, err)
		},
		config.EnvAPIToken: func(t *testing.T, value string) string {
			t.Setenv(config.EnvAPIToken, value)
			token, src, err := credentials.Resolve(emptyKeyring{}, "acme")
			return fmt.Sprintf("%q %q %v", token, src, err)
		},
	}
	reads := loadReads()
	for _, v := range registry(t) {
		read := func(_ *testing.T, value string) string {
			c, err := load(map[string]string{v.Name: value})
			return fmt.Sprintf("%+v %v", c, err)
		}
		if reads[v.Name] == 0 {
			var ok bool
			if read, ok = outside[v.Name]; !ok {
				t.Errorf("%s is read outside Load, and this test has no reader for it", v.Name)
				continue
			}
			delete(outside, v.Name)
		}
		for _, pair := range [][2]string{{"  ", ""}, {" x ", "x"}, {" true ", "true"}} {
			if got, want := read(t, pair[0]), read(t, pair[1]); got != want {
				t.Errorf("%s=%q: %s\nwant %s, as %s=%q", v.Name, pair[0], got, want, v.Name, pair[1])
			}
		}
	}
	for name := range outside {
		t.Errorf("this test has a reader for %s, which Vars does not list as read outside Load", name)
	}
}

// emptyKeyring holds no token, so a token comes from the environment or
// not at all.
type emptyKeyring struct{}

func (emptyKeyring) Get(string, string) (string, error) { return "", nil }
func (emptyKeyring) Set(string, string, string) error   { return nil }
func (emptyKeyring) Delete(string, string) error        { return nil }

// Every PIPEDRIVE_ variable the server's code spells is one EnvVars
// lists, so a variable read outside Load cannot escape the bundle gate.
// So is every one scripts/ spells: the smoke test sets them for the
// server, and a misspelling there tests a configuration nobody asked
// for. The eval harness's own variables, PIPEDRIVE_TEST_ and a name, are
// left out, since the harness reads them itself and the server never
// does. Tests are not read: the integration suite's switch is the
// suite's own. A name is seen as a string literal, or as a chain of
// literals and EnvPrefix joined by +; one built from any other constant,
// or through a format verb, is not seen. The scan must read every root
// and find a name outside the config package, or it read too little.
func TestEveryVariableTheCodeNamesIsInEnvVars(t *testing.T) {
	known := map[string]bool{}
	for _, name := range config.EnvVars() {
		known[name] = true
	}
	name := regexp.MustCompile(`^PIPEDRIVE_[A-Z0-9][A-Z0-9_]*$`)
	found := map[string]string{}
	elsewhere := 0
	fset := token.NewFileSet()
	roots := []string{"../../cmd", "../../internal", "../../scripts"}
	evals := filepath.Join("..", "..", "scripts", "evals") + string(filepath.Separator)
	files := map[string]int{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			files[root]++
			outside := filepath.Dir(path) != filepath.Join("..", "..", "internal", "config")
			harnessOwn := func(word string) bool {
				return strings.HasPrefix(path, evals) && strings.HasPrefix(word, "PIPEDRIVE_TEST_")
			}
			spell := func(s string, at token.Pos) {
				for _, word := range strings.FieldsFunc(s, func(r rune) bool {
					return r != '_' && (r < 'A' || r > 'Z') && (r < '0' || r > '9')
				}) {
					if name.MatchString(word) && !harnessOwn(word) {
						found[word] = fset.Position(at).String()
						if outside {
							elsewhere++
						}
					}
				}
			}
			var visit func(n ast.Node) bool
			visit = func(n ast.Node) bool {
				switch n := n.(type) {
				case *ast.BinaryExpr:
					if n.Op != token.ADD {
						return true
					}
					// A + chain is read in order: each run of literals
					// and EnvPrefix is one string, and any other operand
					// ends the run and is read on its own.
					run, at := "", n.Pos()
					for _, op := range operands(n) {
						if s, ok := joined(op); ok {
							if run == "" {
								at = op.Pos()
							}
							run += s
							continue
						}
						spell(run, at)
						run = ""
						ast.Inspect(op, visit)
					}
					spell(run, at)
					return false
				case *ast.BasicLit:
					if s, err := strconv.Unquote(n.Value); n.Kind == token.STRING && err == nil {
						spell(s, n.Pos())
					}
				}
				return true
			}
			ast.Inspect(f, visit)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range roots {
		if files[root] == 0 {
			t.Errorf("the scan parsed no Go file under %s; it read too little", root)
		}
	}
	if elsewhere == 0 {
		t.Error("the scan found no PIPEDRIVE_ name outside internal/config; it read too little")
	}
	for _, v := range slices.Sorted(maps.Keys(found)) {
		if !known[v] {
			t.Errorf("%s names %s, which EnvVars does not list", found[v], v)
		}
	}
}

// operands is a + chain's operands in order, through parentheses.
func operands(e ast.Expr) []ast.Expr {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return append(operands(e.X), operands(e.Y)...)
		}
	case *ast.ParenExpr:
		if b, ok := e.X.(*ast.BinaryExpr); ok && b.Op == token.ADD {
			return operands(b)
		}
	}
	return []ast.Expr{e}
}

// joined is a + chain of string literals and EnvPrefix as one string.
func joined(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, ok := joined(e.X)
		if !ok {
			return "", false
		}
		y, ok := joined(e.Y)
		return x + y, ok
	case *ast.ParenExpr:
		return joined(e.X)
	case *ast.BasicLit:
		s, err := strconv.Unquote(e.Value)
		return s, e.Kind == token.STRING && err == nil
	case *ast.Ident:
		return config.EnvPrefix, e.Name == "EnvPrefix"
	case *ast.SelectorExpr:
		return config.EnvPrefix, e.Sel.Name == "EnvPrefix"
	}
	return "", false
}
