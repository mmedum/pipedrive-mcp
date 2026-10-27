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

// The credential is marked secret, so the bundle gate holds its field
// to sensitive; nothing else is.
func TestVarsMarkTheSecrets(t *testing.T) {
	var secrets []string
	for _, v := range registry(t) {
		if v.Secret {
			secrets = append(secrets, v.Name)
		}
	}
	if want := []string{"PIPEDRIVE_API_TOKEN"}; !slices.Equal(secrets, want) {
		t.Errorf("secrets %v, want %v", secrets, want)
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

// The scan below reads every non-test Go file under cmd/, internal/ and
// scripts/, where the smoke test and the eval harness set and read
// variables too. Tests are not read: the integration suite's switch is
// the suite's own.

// sourceFile is one Go file the scan read.
type sourceFile struct {
	// dir is the file's package directory from the repository root,
	// such as internal/config.
	dir  string
	file *ast.File
}

// scanRoots are the trees the scan reads, from this package.
var scanRoots = []string{"../../cmd", "../../internal", "../../scripts"}

// configDir is this package's directory, where every name is spelled.
const configDir = "internal/config"

// sources parses every non-test Go file under scanRoots, and refuses a
// root it found none in, since that scan read too little.
func sources(t *testing.T) (*token.FileSet, []sourceFile) {
	t.Helper()
	fset := token.NewFileSet()
	var files []sourceFile
	for _, root := range scanRoots {
		parsed := 0
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			parsed++
			dir := strings.TrimPrefix(filepath.ToSlash(filepath.Dir(path)), "../../")
			files = append(files, sourceFile{dir: dir, file: f})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if parsed == 0 {
			t.Errorf("the scan parsed no Go file under %s; it read too little", root)
		}
	}
	return fset, files
}

// spelling is a word a string in the code spells, and where.
type spelling struct{ word, at string }

// nameRune reports whether r can be part of a variable's name.
func nameRune(r rune) bool {
	return r == '_' || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9')
}

// spellings is every word the strings in f spell. A word is a run of
// capitals, digits and _, bounded by anything else. A string is a
// literal, or a run of a + chain made of literals and EnvPrefix, read as
// one string, in order; any other operand ends the run and is read on
// its own. Each run is read whole, and a word ending in _ is a prefix
// rather than a name, so it is not spelled: EnvPrefix + "ENABLE_" + kind
// spells nothing, EnvPrefix + "WHOLE" + suffix spells PIPEDRIVE_WHOLE,
// and "set " + x + EnvPrefix + "NEW" spells PIPEDRIVE_NEW. A name built from any other
// constant, or through a format verb, is not seen.
func spellings(fset *token.FileSet, f *ast.File) []spelling {
	var out []spelling
	spell := func(s string, at token.Pos) {
		for _, word := range strings.FieldsFunc(s, func(r rune) bool { return !nameRune(r) }) {
			if !strings.HasSuffix(word, "_") {
				out = append(out, spelling{word: word, at: fset.Position(at).String()})
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
			run, at, started := "", n.Pos(), false
			for _, op := range operands(n) {
				if s, ok := joined(op); ok {
					if !started {
						at, started = op.Pos(), true
					}
					run += s
					continue
				}
				spell(run, at)
				run, started = "", false
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
	return out
}

// harnessOwn reports whether a word is one of the eval harness's own
// variables, PIPEDRIVE_TEST_ and a name, which the harness reads itself
// and the server never does.
func harnessOwn(dir, word string) bool {
	return strings.HasPrefix(dir, "scripts/evals") && strings.HasPrefix(word, "PIPEDRIVE_TEST_")
}

// Every PIPEDRIVE_ variable the code spells is one EnvVars lists, so a
// variable read outside Load cannot escape the bundle gate, and a
// misspelling in scripts/ cannot test a configuration nobody asked for.
// The harness's own are left out.
func TestEveryVariableTheCodeNamesIsInEnvVars(t *testing.T) {
	known := map[string]bool{}
	for _, name := range config.EnvVars() {
		known[name] = true
	}
	pattern := regexp.MustCompile(`^PIPEDRIVE_[A-Z0-9][A-Z0-9_]*$`)
	fset, files := sources(t)
	found := map[string]string{}
	for _, f := range files {
		for _, s := range spellings(fset, f.file) {
			if pattern.MatchString(s.word) && !harnessOwn(f.dir, s.word) {
				found[s.word] = s.at
			}
		}
	}
	if len(found) == 0 {
		t.Fatal("the scan found no PIPEDRIVE_ name; it read too little")
	}
	for _, v := range slices.Sorted(maps.Keys(found)) {
		if !known[v] {
			t.Errorf("%s names %s, which EnvVars does not list", found[v], v)
		}
	}
}

// Each variable the server reads is spelled once, in this package, and
// everything else, a message or the smoke test's environment included,
// uses its constant. LOG_LEVEL and LOG_FORMAT carry no prefix, so a
// pattern cannot find them; the names come from EnvVars instead. Never
// seeing one spelled means the scan read too little.
func TestEachVariableIsSpelledOnce(t *testing.T) {
	fset, files := sources(t)
	at := map[string][]string{}
	for _, name := range config.EnvVars() {
		at[name] = nil
	}
	for _, f := range files {
		for _, s := range spellings(fset, f.file) {
			if places, ok := at[s.word]; ok {
				at[s.word] = append(places, s.at)
			}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(at)) {
		switch places := at[name]; {
		case len(places) == 0:
			t.Errorf("the scan never saw %s spelled; it read too little", name)
		case len(places) > 1:
			t.Errorf("%s is spelled %d times, at %s; spell it once in %s and use its constant",
				name, len(places), strings.Join(places, ", "), configDir)
		case !strings.Contains(filepath.ToSlash(places[0]), configDir+"/"):
			t.Errorf("%s is spelled at %s; spell it in %s", name, places[0], configDir)
		}
	}
}

// scriptsOwn is every variable a script reads that the server does not,
// with whose it is. os.Getenv may read these and nothing else.
var scriptsOwn = map[string]string{
	"SMOKE_HOLD":                 "the smoke gate's: how long it holds the server's stdin open",
	"PIPEDRIVE_TEST_PIPELINE_ID": "the eval harness's: the scratch pipeline its tasks close deals in",
}

// readsFloor is how many reads of a variable the scan finds outside this
// package today, on purpose: the floor moves with them, so a scan that
// finds no call cannot pass.
const readsFloor = 8

// Every read of a variable outside this package goes through
// config.Lookup, so the spaces rule holds for it, and names a variable
// EnvVars lists. os.Getenv and os.LookupEnv read only a script's own
// variable, declared in scriptsOwn, so an unprefixed variable the server
// read with either would not escape the registry. A variable named
// through a constant is followed to it.
func TestEveryReadGoesThroughLookup(t *testing.T) {
	known := map[string]bool{}
	for _, name := range config.EnvVars() {
		known[name] = true
	}
	fset, files := sources(t)
	consts := constants(files)
	reads := 0
	seen := map[string]bool{}
	for _, f := range files {
		if f.dir == configDir {
			continue
		}
		for _, r := range envReads(fset, f, consts) {
			reads++
			seen[r.name] = true
			switch {
			case r.name == "":
				t.Errorf("%s: %s reads a variable this scan cannot name; name it with a constant", r.at, r.call)
			case r.call == "config.Lookup" && !known[r.name]:
				t.Errorf("%s: config.Lookup reads %s, which EnvVars does not list", r.at, r.name)
			case r.call != "config.Lookup" && known[r.name]:
				t.Errorf("%s: %s reads %s, which the server reads; read it through config.Lookup", r.at, r.call, r.name)
			case r.call != "config.Lookup" && scriptsOwn[r.name] == "":
				t.Errorf("%s: %s reads %s, which is neither the server's nor in scriptsOwn", r.at, r.call, r.name)
			}
		}
	}
	if reads < readsFloor {
		t.Errorf("the scan found %d reads outside %s, want at least %d; it read too little", reads, configDir, readsFloor)
	}
	for _, name := range slices.Sorted(maps.Keys(scriptsOwn)) {
		if !seen[name] {
			t.Errorf("scriptsOwn names %s, which nothing reads", name)
		}
	}
}

// constSpec is a package-level constant's value and the directory of
// the package it is in.
type constSpec struct {
	value ast.Expr
	dir   string
}

// constants is every package-level constant with one value in files,
// keyed by its package directory and name: internal/config.DomainEnv.
func constants(files []sourceFile) map[string]constSpec {
	out := map[string]constSpec{}
	for _, f := range files {
		for _, decl := range f.file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				if len(vs.Values) != len(vs.Names) {
					continue
				}
				for i, name := range vs.Names {
					out[f.dir+"."+name.Name] = constSpec{value: vs.Values[i], dir: f.dir}
				}
			}
		}
	}
	return out
}

// resolve is the string e names, in the package at dir: a literal, a
// constant of that package or of config, or a + chain of them.
func resolve(e ast.Expr, dir string, consts map[string]constSpec) (string, bool) {
	switch e := e.(type) {
	case *ast.BasicLit:
		s, err := strconv.Unquote(e.Value)
		return s, e.Kind == token.STRING && err == nil
	case *ast.ParenExpr:
		return resolve(e.X, dir, consts)
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, ok := resolve(e.X, dir, consts)
		if !ok {
			return "", false
		}
		y, ok := resolve(e.Y, dir, consts)
		return x + y, ok
	case *ast.Ident:
		if c, ok := consts[dir+"."+e.Name]; ok {
			return resolve(c.value, c.dir, consts)
		}
	case *ast.SelectorExpr:
		if pkg, ok := e.X.(*ast.Ident); ok && pkg.Name == "config" {
			if c, ok := consts[configDir+"."+e.Sel.Name]; ok {
				return resolve(c.value, c.dir, consts)
			}
		}
	}
	return "", false
}

// envRead is one call that reads a variable: which call, the variable,
// "" when the argument cannot be resolved, and where.
type envRead struct{ call, name, at string }

// envReads is every os.Getenv, os.LookupEnv and config.Lookup call in f.
func envReads(fset *token.FileSet, f sourceFile, consts map[string]constSpec) []envRead {
	var out []envRead
	ast.Inspect(f.file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		arg := -1
		switch name := pkg.Name + "." + sel.Sel.Name; name {
		case "os.Getenv", "os.LookupEnv":
			arg = 0
		case "config.Lookup":
			arg = 1
		}
		if arg < 0 || len(call.Args) <= arg {
			return true
		}
		name, _ := resolve(call.Args[arg], f.dir, consts)
		out = append(out, envRead{call: pkg.Name + "." + sel.Sel.Name, name: name, at: fset.Position(call.Pos()).String()})
		return true
	})
	return out
}

// The name scan reads a + chain in order, each run whole: a word ending
// in _ is a prefix, not a name, and a run after one is still read.
func TestTheScanReadsAChainInOrder(t *testing.T) {
	for _, tc := range []struct {
		expr string
		want []string
	}{
		{`EnvPrefix + "ENABLE_" + kind`, nil},
		{`"set " + x + EnvPrefix + "NEW"`, []string{"PIPEDRIVE_NEW"}},
		{`EnvPrefix + "WHOLE" + suffix`, []string{"PIPEDRIVE_WHOLE"}},
		{`"PIPEDRIVE_OLD is gone; " + x`, []string{"PIPEDRIVE_OLD"}},
		{`config.EnvPrefix + ("A_" + kind) + "_B"`, []string{"_B"}},
		{`"LOG_LEVEL"`, []string{"LOG_LEVEL"}},
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "x.go", "package x\nvar _ = "+tc.expr+"\n", 0)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, s := range spellings(fset, f) {
			got = append(got, s.word)
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s spells %q, want %q", tc.expr, got, tc.want)
		}
	}
}

// A read names its variable however the call spells it: a literal, a
// constant of its own package, one of config's, or one defined by
// another.
func TestTheScanNamesEachRead(t *testing.T) {
	const src = `package x
const own = config.EnvPrefix + "OWN"
const alias = config.DomainEnv
func f() {
	os.Getenv("LOG_LEVEL")
	os.LookupEnv(own)
	config.Lookup(os.Getenv, alias)
	config.Lookup(os.Getenv, name)
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "x.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := parser.ParseFile(fset, "config.go",
		"package config\nconst EnvPrefix = \"PIPEDRIVE_\"\nconst DomainEnv = EnvPrefix + \"COMPANY_DOMAIN\"\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	files := []sourceFile{{dir: "x", file: f}, {dir: configDir, file: cfg}}
	reads := envReads(fset, files[0], constants(files))
	got := make([]string, 0, len(reads))
	for _, r := range reads {
		got = append(got, r.call+" "+r.name)
	}
	want := []string{"os.Getenv LOG_LEVEL", "os.LookupEnv PIPEDRIVE_OWN",
		"config.Lookup PIPEDRIVE_COMPANY_DOMAIN", "config.Lookup "}
	if !slices.Equal(got, want) {
		t.Errorf("reads = %q, want %q", got, want)
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
