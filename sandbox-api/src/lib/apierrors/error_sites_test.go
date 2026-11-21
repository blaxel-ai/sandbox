package apierrors

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Every error a client gets carries a registered code. Handlers answer with
// SendError or WriteError, and TestNoErrorBodyBypassesTheRegistry
// (catalog_test.go) refuses hand-built error bodies in src/handler and
// src/api. TestErrorSitesUseTheRegistry covers the rest of the module: it
// counts the sites that send an error without a registered code, of the kinds
// below, and compares them with errorSitesFile, which lists the sites that
// predate the check, per file. The test fails when a file has more sites of a
// kind than listed (a new one), and when it has fewer (lower its count in the
// same change, so the list only gets shorter). In CI, ERROR_SITES_BASE names
// the base branch's list, and the test also fails when a count is higher than
// there. After converting sites, rewrite the list:
//
//	UPDATE_ERROR_SITES=1 go test ./src/lib/apierrors -run TestErrorSitesUseTheRegistry

const errorSitesFile = "testdata/unregistered_error_sites.txt"

// The kinds of site that send an error without a registered code.
const (
	// directSite writes an error straight to the client, around the writers:
	// gin's c.JSON, c.AbortWithStatusJSON, c.String, c.AbortWithStatus and the
	// like with an error status or an error body (gin.H{"error": ...},
	// ErrorResponse{...}), http.Error, and WriteHeader with an error status.
	directSite = "direct"
	// entrySite declares an apierrors.Entry outside this package: a code that
	// is not in the registry, the export or the reference. A zero Entry{} is
	// not one.
	entrySite = "entry"
)

var siteKinds = []string{directSite, entrySite}

// siteFixes says how to send each kind of site with a registered code.
var siteFixes = map[string]string{
	directSite: "answer with SendError or WriteError and a registered code",
	entrySite:  "register the entry in src/lib/apierrors/registry.go",
}

// skippedDirs are not scanned: the registry itself.
var skippedDirs = []string{"src/lib/apierrors"}

// writeMethods write a response whose status is their first argument, as gin's
// Context and http.ResponseWriter do.
var writeMethods = map[string]bool{
	"JSON": true, "AbortWithStatusJSON": true, "IndentedJSON": true, "PureJSON": true, "SecureJSON": true,
	"AsciiJSON": true, "JSONP": true, "String": true, "Data": true, "DataFromReader": true, "XML": true,
	"YAML": true, "TOML": true, "ProtoBuf": true, "Render": true, "AbortWithStatus": true,
	"AbortWithError": true, "Status": true, "WriteHeader": true,
}

// errorStatuses are net/http's 4xx and 5xx constants.
var errorStatuses = map[string]bool{
	"StatusBadRequest": true, "StatusUnauthorized": true, "StatusPaymentRequired": true, "StatusForbidden": true,
	"StatusNotFound": true, "StatusMethodNotAllowed": true, "StatusNotAcceptable": true,
	"StatusProxyAuthRequired": true, "StatusRequestTimeout": true, "StatusConflict": true, "StatusGone": true,
	"StatusLengthRequired": true, "StatusPreconditionFailed": true, "StatusRequestEntityTooLarge": true,
	"StatusRequestURITooLong": true, "StatusUnsupportedMediaType": true,
	"StatusRequestedRangeNotSatisfiable": true, "StatusExpectationFailed": true, "StatusTeapot": true,
	"StatusMisdirectedRequest": true, "StatusUnprocessableEntity": true, "StatusLocked": true,
	"StatusFailedDependency": true, "StatusTooEarly": true, "StatusUpgradeRequired": true,
	"StatusPreconditionRequired": true, "StatusTooManyRequests": true,
	"StatusRequestHeaderFieldsTooLarge": true, "StatusUnavailableForLegalReasons": true,
	"StatusInternalServerError": true, "StatusNotImplemented": true, "StatusBadGateway": true,
	"StatusServiceUnavailable": true, "StatusGatewayTimeout": true, "StatusHTTPVersionNotSupported": true,
	"StatusVariantAlsoNegotiates": true, "StatusInsufficientStorage": true, "StatusLoopDetected": true,
	"StatusNotExtended": true, "StatusNetworkAuthenticationRequired": true,
}

// errorSite is one kind of site in one file, relative to the module.
type errorSite struct{ file, kind string }

// scanErrorSites counts the sites of each kind in the Go files under root,
// leaving out tests, generated files, nested modules and skippedDirs.
func scanErrorSites(root string) (map[errorSite]int, error) {
	counts := map[errorSite]int{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			name := d.Name()
			if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil || strings.HasPrefix(name, ".") ||
				name == "testdata" || name == "vendor" || slices.Contains(skippedDirs, rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		if ast.IsGenerated(file) {
			return nil
		}
		for kind, n := range fileErrorSites(file) {
			counts[errorSite{rel, kind}] += n
		}
		return nil
	})
	return counts, err
}

// fileErrorSites counts the sites of each kind in file.
func fileErrorSites(file *ast.File) map[string]int {
	counts := map[string]int{}
	ast.Inspect(file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			method, isMethod := n.Fun.(*ast.SelectorExpr)
			switch {
			case isQualified(n.Fun, "http", "Error"):
				counts[directSite]++
			case isMethod && !isPackage(method.X, "http") && writeMethods[method.Sel.Name] && len(n.Args) > 0 &&
				(isErrorStatus(n.Args[0]) || len(n.Args) > 1 && isErrorBody(n.Args[1])):
				counts[directSite]++
			}
		case *ast.CompositeLit:
			// A zero Entry{} is a "no code" result, not a code.
			if isQualified(n.Type, "apierrors", "Entry") && len(n.Elts) > 0 {
				counts[entrySite]++
			}
		}
		return true
	})
	return counts
}

// calledName is the function or method a call names.
func calledName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name
	case *ast.SelectorExpr:
		return fun.Sel.Name
	}
	return ""
}

// receiverCall is the call a method call is made on, as in a chain
// x.A().B(); nil when the receiver is not a call.
func receiverCall(call *ast.CallExpr) *ast.CallExpr {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if inner, ok := sel.X.(*ast.CallExpr); ok {
			return inner
		}
	}
	return nil
}

func isPackage(x ast.Expr, pkg string) bool {
	ident, ok := x.(*ast.Ident)
	return ok && ident.Name == pkg
}

// isQualified reports whether x is pkg.name.
func isQualified(x ast.Expr, pkg, name string) bool {
	sel, ok := x.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name && isPackage(sel.X, pkg)
}

// typeName is the name of a composite literal's type, with or without its
// package.
func typeName(x ast.Expr) string {
	switch typ := x.(type) {
	case *ast.Ident:
		return typ.Name
	case *ast.SelectorExpr:
		return typ.Sel.Name
	case *ast.UnaryExpr:
		return typeName(typ.X)
	}
	return ""
}

// isErrorStatus reports whether x is a 4xx or 5xx status: an http.Status
// constant or an integer literal.
func isErrorStatus(x ast.Expr) bool {
	switch x := x.(type) {
	case *ast.BasicLit:
		status, err := strconv.Atoi(x.Value)
		return x.Kind == token.INT && err == nil && status >= 400 && status <= 599
	case *ast.SelectorExpr:
		return isPackage(x.X, "http") && errorStatuses[x.Sel.Name]
	}
	return false
}

// isErrorBody reports whether x is a hand-built error body: an
// ErrorResponse literal, or a map literal (gin.H included) with an "error"
// key.
func isErrorBody(x ast.Expr) bool {
	if unary, ok := x.(*ast.UnaryExpr); ok {
		x = unary.X
	}
	lit, ok := x.(*ast.CompositeLit)
	if !ok {
		return false
	}
	if typeName(lit.Type) == "ErrorResponse" {
		return true
	}
	if _, isMap := lit.Type.(*ast.MapType); !isMap && !isQualified(lit.Type, "gin", "H") {
		return false
	}
	return fieldValue(lit, "error") != nil
}

// fieldValue is the value of key in a struct or map literal: a field name or
// a string key.
func fieldValue(lit *ast.CompositeLit, key string) ast.Expr {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		switch k := kv.Key.(type) {
		case *ast.Ident:
			if k.Name == key {
				return kv.Value
			}
		case *ast.BasicLit:
			if k.Kind == token.STRING && k.Value == strconv.Quote(key) {
				return kv.Value
			}
		}
	}
	return nil
}

// readErrorSites reads a list in errorSitesFile's format: comment lines, one
// of them "# Total: <n> ...", then one "<count> <kind> <file>" line per file
// and kind. It returns the counts and the stated total.
func readErrorSites(text string) (map[errorSite]int, int, error) {
	counts := map[errorSite]int{}
	total := -1
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# Total: ") {
			if _, err := fmt.Sscanf(line, "# Total: %d", &total); err != nil {
				return nil, 0, fmt.Errorf("line %d: %w", i+1, err)
			}
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 3 || !slices.Contains(siteKinds, fields[1]) {
			return nil, 0, fmt.Errorf("line %d: %q is not \"<count> <kind> <file>\"", i+1, line)
		}
		n, err := strconv.Atoi(fields[0])
		if err != nil || n <= 0 {
			return nil, 0, fmt.Errorf("line %d: count %q", i+1, fields[0])
		}
		site := errorSite{fields[2], fields[1]}
		if _, dup := counts[site]; dup {
			return nil, 0, fmt.Errorf("line %d: %s %s is listed twice", i+1, site.kind, site.file)
		}
		counts[site] = n
	}
	if total < 0 {
		return nil, 0, fmt.Errorf("no \"# Total: <n>\" line")
	}
	return counts, total, nil
}

// formatErrorSites writes counts in errorSitesFile's format.
func formatErrorSites(counts map[errorSite]int) string {
	byKind := map[string]int{}
	total := 0
	for site, n := range counts {
		byKind[site.kind] += n
		total += n
	}
	var kinds []string
	for _, kind := range siteKinds {
		kinds = append(kinds, fmt.Sprintf("%s %d", kind, byKind[kind]))
	}
	var b strings.Builder
	b.WriteString(`# Error sites that send an error without a registered code, per file and
# kind: "<count> <kind> <file>", files relative to sandbox-api/. Kinds: direct
# (an error written straight to the client, around SendError and WriteError),
# entry (an Entry declared outside the registry). See error_sites_test.go.
#
# TestErrorSitesUseTheRegistry fails when a file has more sites than listed, and
# when it has fewer: the list only gets shorter. Never add a line or raise a
# count; answer with SendError or WriteError and a registered code instead.
# After converting sites, rewrite this file:
#   UPDATE_ERROR_SITES=1 go test ./src/lib/apierrors -run TestErrorSitesUseTheRegistry
#
`)
	fmt.Fprintf(&b, "# Total: %d (%s)\n\n", total, strings.Join(kinds, ", "))
	sites := sortedSites(counts)
	for _, site := range sites {
		if counts[site] > 0 {
			fmt.Fprintf(&b, "%d %s %s\n", counts[site], site.kind, site.file)
		}
	}
	return b.String()
}

func sortedSites(maps ...map[errorSite]int) []errorSite {
	var sites []errorSite
	for _, m := range maps {
		for site := range m {
			if !slices.Contains(sites, site) {
				sites = append(sites, site)
			}
		}
	}
	sort.Slice(sites, func(i, j int) bool {
		if sites[i].file != sites[j].file {
			return sites[i].file < sites[j].file
		}
		return slices.Index(siteKinds, sites[i].kind) < slices.Index(siteKinds, sites[j].kind)
	})
	return sites
}

// errorSitesDrift lists where the sites found differ from the list.
func errorSitesDrift(found, listed map[errorSite]int) []string {
	var problems []string
	for _, site := range sortedSites(found, listed) {
		switch f, l := found[site], listed[site]; {
		case f > l:
			problems = append(problems, fmt.Sprintf("%s: %d %s sites send an error without a registered code, %d listed in %s: %s",
				site.file, f, site.kind, l, errorSitesFile, siteFixes[site.kind]))
		case f < l:
			problems = append(problems, fmt.Sprintf("%s: %d %s sites, %d listed in %s: lower its count (the list only gets shorter)",
				site.file, f, site.kind, l, errorSitesFile))
		}
	}
	return problems
}

// errorSitesGrowth lists the counts that are higher in listed than in base,
// the base branch's list.
func errorSitesGrowth(listed, base map[errorSite]int) []string {
	var problems []string
	for _, site := range sortedSites(listed) {
		if listed[site] > base[site] {
			problems = append(problems, fmt.Sprintf("%s: %d %s sites listed, %d on the base branch: the list only gets shorter, %s",
				site.file, listed[site], site.kind, base[site], siteFixes[site.kind]))
		}
	}
	return problems
}

func TestErrorSitesUseTheRegistry(t *testing.T) {
	found, err := scanErrorSites(moduleRoot)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_ERROR_SITES") != "" {
		if err := os.WriteFile(errorSitesFile, []byte(formatErrorSites(found)), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	text, err := os.ReadFile(errorSitesFile)
	if err != nil {
		t.Fatal(err)
	}
	listed, total, err := readErrorSites(string(text))
	if err != nil {
		t.Fatalf("%s: %v", errorSitesFile, err)
	}
	for _, problem := range errorSitesDrift(found, listed) {
		t.Error(problem)
	}
	sum := 0
	for _, n := range listed {
		sum += n
	}
	if total != sum {
		t.Errorf("%s says Total: %d, its lines add up to %d", errorSitesFile, total, sum)
	}
	if path := os.Getenv("ERROR_SITES_BASE"); path != "" {
		baseText, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		base, _, err := readErrorSites(string(baseText))
		if err != nil {
			t.Fatalf("the base branch's %s: %v", errorSitesFile, err)
		}
		for _, problem := range errorSitesGrowth(listed, base) {
			t.Error(problem)
		}
	}
}

func TestFileErrorSitesFindsEachKind(t *testing.T) {
	const src = `package handler

func bare(c *gin.Context, w http.ResponseWriter, status, code int, err error) {
	c.JSON(http.StatusMethodNotAllowed, gin.H{"jsonrpc": "2.0"})
	c.JSON(status, gin.H{"error": err.Error()})
	c.AbortWithStatusJSON(status, &ErrorResponse{Error: "no"})
	c.AbortWithStatus(401)
	c.String(500, "failed")
	http.Error(w, "no", code)
	w.WriteHeader(http.StatusBadGateway)
	h.SendError(c, apierrors.Entry{Code: "AD_HOC", Status: 400}, err)
}

func registered(c *gin.Context, h *BaseHandler, entry apierrors.Entry, err error) {
	c.JSON(http.StatusOK, gin.H{"ok": true})
	c.JSON(entry.Status, entry.Body(entry.Status, "x", meta))
	w.WriteHeader(resp.StatusCode)
	h.SendError(c, apierrors.ProcessNotFound, err)
	WriteError(c, apierrors.InvalidRequestBody, "bad body")
	_ = apierrors.Entry{}
}
`
	file, err := parser.ParseFile(token.NewFileSet(), "fixture.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := fileErrorSites(file), map[string]int{directSite: 7, entrySite: 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestErrorSitesListOnlyGetsShorter(t *testing.T) {
	listed := map[errorSite]int{{"src/a.go", directSite}: 2, {"src/b.go", entrySite}: 1}
	with := func(site errorSite, n int) map[errorSite]int {
		changed := maps.Clone(listed)
		changed[site] = n
		return changed
	}
	expect := func(t *testing.T, got []string, want ...string) {
		t.Helper()
		if !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
			t.Errorf("got %q\nwant %q", got, want)
		}
	}

	t.Run("in step", func(t *testing.T) {
		expect(t, errorSitesDrift(listed, listed))
		expect(t, errorSitesGrowth(listed, listed))
	})
	t.Run("a new site", func(t *testing.T) {
		expect(t, errorSitesDrift(with(errorSite{"src/c.go", directSite}, 1), listed),
			"src/c.go: 1 direct sites send an error without a registered code, 0 listed in "+errorSitesFile+
				": answer with SendError or WriteError and a registered code")
	})
	t.Run("a converted site", func(t *testing.T) {
		expect(t, errorSitesDrift(with(errorSite{"src/a.go", directSite}, 1), listed),
			"src/a.go: 1 direct sites, 2 listed in "+errorSitesFile+": lower its count (the list only gets shorter)")
	})
	t.Run("a list that grew against the base branch", func(t *testing.T) {
		expect(t, errorSitesGrowth(with(errorSite{"src/a.go", directSite}, 3), listed),
			"src/a.go: 3 direct sites listed, 2 on the base branch: the list only gets shorter, "+
				"answer with SendError or WriteError and a registered code")
	})
	t.Run("the file format", func(t *testing.T) {
		read, total, err := readErrorSites(formatErrorSites(listed))
		if err != nil || !reflect.DeepEqual(read, listed) || total != 3 {
			t.Errorf("read %v, total %d, err %v", read, total, err)
		}
		if _, _, err := readErrorSites("# Total: 1\n1 unknown src/a.go\n"); err == nil {
			t.Error("an unknown kind reads without an error")
		}
	})
}
