package apierrors

import (
	"encoding/csv"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// catalog.csv is the sandbox API's error catalog: every error site, the code
// it sends, or why it sends none. These tests keep it in step with the
// registry and with the code.

// moduleRoot is the sandbox-api directory, which the catalog's sites are
// relative to.
const moduleRoot = "../../.."

// scannedDirs hold every handler that answers API errors.
var scannedDirs = []string{"src/handler", "src/api"}

var catalogHeader = []string{"id", "family", "status", "code", "message", "retryable", "dispatch_state", "safe_to_retry_request", "decision", "reason", "sites"}

type catalogRow struct {
	id, family, status, code, message, retryable, dispatchState, safe, decision, reason string
	sites                                                                               []string
}

func readCatalog(t *testing.T) []catalogRow {
	t.Helper()
	f, err := os.Open("catalog.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(records) == 0 || !slices.Equal(records[0], catalogHeader) {
		t.Fatalf("catalog.csv header is not %v", catalogHeader)
	}
	rows := make([]catalogRow, 0, len(records)-1)
	for _, r := range records[1:] {
		rows = append(rows, catalogRow{r[0], r[1], r[2], r[3], r[4], r[5], r[6], r[7], r[8], r[9], strings.Split(r[10], ";")})
	}
	return rows
}

func TestCatalogAgreesWithTheRegistry(t *testing.T) {
	rows := readCatalog(t)
	byID := map[string]catalogRow{}
	for _, row := range rows {
		if !catalogPattern.MatchString(row.id) {
			t.Errorf("row id %q", row.id)
		}
		if _, dup := byID[row.id]; dup {
			t.Errorf("row %s is listed twice", row.id)
		}
		byID[row.id] = row
		if row.family == "" || row.status == "" || row.message == "" || row.reason == "" || len(row.sites) == 0 || row.sites[0] == "" {
			t.Errorf("row %s: family, status, message, reason and sites are required", row.id)
		}
		switch row.decision {
		case "unchanged":
			if row.code != "" {
				t.Errorf("row %s is unchanged but names code %s", row.id, row.code)
			}
		case "registered":
			entry, ok := Lookup(row.code)
			if !ok {
				t.Errorf("row %s: code %q is not registered", row.id, row.code)
				continue
			}
			if row.status != strconv.Itoa(entry.Status) || row.retryable != strconv.FormatBool(entry.Retryable) ||
				row.dispatchState != string(entry.DispatchState) || row.safe != strconv.FormatBool(entry.SafeToRetryRequest()) {
				t.Errorf("row %s does not match %s: %+v", row.id, entry.Code, row)
			}
			if !slices.Contains(entry.Catalog, row.id) {
				t.Errorf("row %s is not in %s.Catalog", row.id, entry.Code)
			}
		default:
			t.Errorf("row %s: decision %q is neither registered nor unchanged", row.id, row.decision)
		}
	}
	for _, entry := range Entries() {
		for _, id := range entry.Catalog {
			if row, ok := byID[id]; !ok || row.code != entry.Code {
				t.Errorf("%s lists row %s, which is not one of its rows", entry.Code, id)
			}
		}
	}
}

// TestCatalogCoversEverySite finds every function that answers with a
// registered code (any apierrors.<Entry> it references) and checks that the
// code's catalog rows list exactly those functions.
func TestCatalogCoversEverySite(t *testing.T) {
	codes := registryCodes(t)
	inCode := map[string][]string{}
	scanHandlers(t, func(site string, fn *ast.FuncDecl, file *ast.File) {
		ast.Inspect(fn, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "apierrors" {
					if code, ok := codes[sel.Sel.Name]; ok && !slices.Contains(inCode[code], site) {
						inCode[code] = append(inCode[code], site)
					}
				}
			}
			return true
		})
	})
	inCatalog := map[string][]string{}
	for _, row := range readCatalog(t) {
		if row.decision != "registered" {
			continue
		}
		for _, site := range row.sites {
			if !slices.Contains(inCatalog[row.code], site) {
				inCatalog[row.code] = append(inCatalog[row.code], site)
			}
		}
	}
	for _, code := range sortedKeys(inCode, inCatalog) {
		sent, listed := inCode[code], inCatalog[code]
		for _, site := range sent {
			if !slices.Contains(listed, site) {
				t.Errorf("%s answers %s: add it to a catalog.csv row of the code", site, code)
			}
		}
		for _, site := range listed {
			if !slices.Contains(sent, site) {
				t.Errorf("catalog.csv lists %s for %s, which no longer answers it", site, code)
			}
		}
	}
}

// TestNoErrorBodyBypassesTheRegistry: every error response goes through the
// registry (SendError, WriteError, ErrorBody), so none is built by hand.
func TestNoErrorBodyBypassesTheRegistry(t *testing.T) {
	scanHandlers(t, func(site string, fn *ast.FuncDecl, file *ast.File) {
		ast.Inspect(fn, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			switch typ := lit.Type.(type) {
			case *ast.Ident:
				if typ.Name == "ErrorResponse" {
					t.Errorf("%s builds an ErrorResponse: answer with SendError and a registered code", site)
				}
			case *ast.SelectorExpr:
				if typ.Sel.Name == "ErrorResponse" {
					t.Errorf("%s builds an ErrorResponse: answer with WriteError and a registered code", site)
				}
				if pkg, ok := typ.X.(*ast.Ident); ok && pkg.Name == "gin" && typ.Sel.Name == "H" {
					for _, elt := range lit.Elts {
						if kv, ok := elt.(*ast.KeyValueExpr); ok {
							if key, ok := kv.Key.(*ast.BasicLit); ok && key.Value == `"error"` {
								t.Errorf("%s answers a gin.H error: answer with SendError and a registered code", site)
							}
						}
					}
				}
			}
			return true
		})
	})
}

// registryCodes maps each entry's variable name in registry.go to its code.
func registryCodes(t *testing.T) map[string]string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "registry.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	codes := map[string]string{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.ValueSpec)
		if !ok || len(spec.Values) != 1 {
			return true
		}
		lit, ok := spec.Values[0].(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Code" {
				if value, ok := kv.Value.(*ast.BasicLit); ok {
					code, err := strconv.Unquote(value.Value)
					if err != nil {
						t.Fatal(err)
					}
					codes[spec.Names[0].Name] = code
				}
			}
		}
		return true
	})
	if len(codes) != len(entries) {
		t.Fatalf("found %d entries in registry.go, the registry has %d", len(codes), len(entries))
	}
	return codes
}

// scanHandlers calls visit for every function of the scanned directories,
// with its site as catalog.csv names it (path:function).
func scanHandlers(t *testing.T, visit func(site string, fn *ast.FuncDecl, file *ast.File)) {
	t.Helper()
	for _, dir := range scannedDirs {
		paths, err := filepath.Glob(filepath.Join(moduleRoot, dir, "*.go"))
		if err != nil || len(paths) == 0 {
			t.Fatalf("no Go files in %s: %v", dir, err)
		}
		for _, path := range paths {
			if strings.HasSuffix(path, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			rel := filepath.ToSlash(filepath.Join(dir, filepath.Base(path)))
			for _, decl := range file.Decls {
				if fn, ok := decl.(*ast.FuncDecl); ok {
					visit(rel+":"+fn.Name.Name, fn, file)
				}
			}
		}
	}
}

func sortedKeys(maps ...map[string][]string) []string {
	var keys []string
	for _, m := range maps {
		for key := range m {
			if !slices.Contains(keys, key) {
				keys = append(keys, key)
			}
		}
	}
	sort.Strings(keys)
	return keys
}
