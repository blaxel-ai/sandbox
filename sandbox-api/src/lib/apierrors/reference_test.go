package apierrors

import (
	"fmt"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The error codes reference (ReferenceURL) lists every registered code in a
// table generated from registry.json by scripts/error-codes/generate.mjs in
// blaxel-ai/docs. TestErrorCodesReference reads that page and fails when it no
// longer matches the registry. .github/workflows/error-codes-reference.yml
// checks the page out and passes its path in ERROR_CODES_REFERENCE. Locally,
// against a docs checkout:
//
//	ERROR_CODES_REFERENCE=<docs>/troubleshooting/error-codes.mdx go test ./src/lib/apierrors -run TestErrorCodesReference

// referenceSection names the sandbox API's generated table on the page.
const referenceSection = "sandbox-api"

// referenceRow is a code row of a generated table, as far as the drift check
// reads it.
type referenceRow struct {
	Anchor        string
	Code          string
	Status        int
	Retryable     bool
	DispatchState string
}

var (
	// referenceRowPattern reads the anchor, code, HTTP status, retryable and
	// dispatch state cells of a generated row.
	referenceRowPattern = regexp.MustCompile("^\\|\\s*<span id=\"([a-z0-9-]+)\"></span>\x60([A-Z0-9_]+)\x60\\s*\\|\\s*(\\d{3})\\s*\\|\\s*(\\*\\*Yes\\*\\*|No)[^|]*\\|\\s*\x60([a-z_]+)\x60\\s*\\|")
	referenceIDPattern  = regexp.MustCompile(`\bid="([a-z0-9-]+)"`)
	// anchorPattern is a code's anchor: its code in lower case, with hyphens.
	anchorPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)
)

// parseReference returns the rows of section's generated table and every
// anchor the page defines.
func parseReference(page, section string) ([]referenceRow, map[string]bool, error) {
	begin := "{/* BEGIN GENERATED " + section + ":"
	end := "{/* END GENERATED " + section + " */}"
	start, stop := strings.Index(page, begin), strings.Index(page, end)
	if start < 0 || stop < start || strings.Count(page, begin) != 1 || strings.Count(page, end) != 1 {
		return nil, nil, fmt.Errorf("the page has no single %q ... %q block", begin, end)
	}
	var rows []referenceRow
	for line := range strings.SplitSeq(page[start:stop], "\n") {
		m := referenceRowPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		status, err := strconv.Atoi(m[3])
		if err != nil {
			return nil, nil, fmt.Errorf("%s: status %q: %w", m[2], m[3], err)
		}
		rows = append(rows, referenceRow{Anchor: m[1], Code: m[2], Status: status, Retryable: m[4] == "**Yes**", DispatchState: m[5]})
	}
	ids := map[string]bool{}
	for _, m := range referenceIDPattern.FindAllStringSubmatch(page, -1) {
		ids[m[1]] = true
	}
	return rows, ids, nil
}

// referenceDrift lists where the page's section differs from entries: a
// registered code missing from it, a code on it that is no longer registered,
// a status, retryable or dispatch state that changed, and a docs_url anchor
// the page does not define.
func referenceDrift(page, section string, entries []Entry) []string {
	rows, ids, err := parseReference(page, section)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	onPage := map[string]referenceRow{}
	for _, row := range rows {
		if _, dup := onPage[row.Code]; dup {
			problems = append(problems, row.Code+" is on the page twice")
		}
		onPage[row.Code] = row
	}
	registered := map[string]bool{}
	for _, entry := range entries {
		registered[entry.Code] = true
		problems = append(problems, docsProblems(entry)...)
		row, ok := onPage[entry.Code]
		if !ok {
			problems = append(problems, entry.Code+" is registered but missing from the page")
			continue
		}
		if row.Status != entry.Status {
			problems = append(problems, fmt.Sprintf("%s: the page says HTTP %d, the registry %d", entry.Code, row.Status, entry.Status))
		}
		if row.Retryable != entry.Retryable {
			problems = append(problems, fmt.Sprintf("%s: the page says retryable %t, the registry %t", entry.Code, row.Retryable, entry.Retryable))
		}
		if row.DispatchState != string(entry.DispatchState) {
			problems = append(problems, fmt.Sprintf("%s: the page says %s, the registry %s", entry.Code, row.DispatchState, entry.DispatchState))
		}
		if anchor, onReference := referenceAnchor(entry); onReference && !ids[anchor] {
			problems = append(problems, fmt.Sprintf("%s sends docs_url %s, an anchor the page does not define", entry.Code, entry.ResolvedDocsURL()))
		}
	}
	for _, row := range rows {
		if !registered[row.Code] {
			problems = append(problems, row.Code+" is on the page but no longer registered")
		}
	}
	sort.Strings(problems)
	return problems
}

// docsProblems says what keeps entry from a usable row on the reference: no
// action to show, or a docs_url that is neither an anchor on the reference
// (the code's own, by default) nor a page. TestEveryCodeHasADocsAnchorAndAnAction
// runs it on the registry; referenceDrift also checks the anchor is on the page.
func docsProblems(entry Entry) []string {
	var problems []string
	if strings.TrimSpace(entry.Action) == "" {
		problems = append(problems, entry.Code+" has no action")
	}
	docsURL := entry.ResolvedDocsURL()
	anchor, onReference := referenceAnchor(entry)
	switch {
	case onReference && !anchorPattern.MatchString(anchor), !onReference && strings.HasPrefix(docsURL, ReferenceURL):
		problems = append(problems, fmt.Sprintf("%s sends docs_url %s, which names no code anchor on the reference", entry.Code, docsURL))
	case !onReference:
		if page, err := url.Parse(docsURL); err != nil || page.Scheme != "https" || page.Host == "" {
			problems = append(problems, fmt.Sprintf("%s sends docs_url %q, which is not a page", entry.Code, docsURL))
		}
	}
	return problems
}

// referenceAnchor is the anchor entry's docs_url points at on the reference,
// when it points there.
func referenceAnchor(entry Entry) (string, bool) {
	return strings.CutPrefix(entry.ResolvedDocsURL(), ReferenceURL+"#")
}

// TestEveryCodeHasADocsAnchorAndAnAction runs without the page, so a code
// that could not get a row fails in every test run, not only in the
// reference check.
func TestEveryCodeHasADocsAnchorAndAnAction(t *testing.T) {
	for _, entry := range Entries() {
		for _, problem := range docsProblems(entry) {
			t.Error(problem)
		}
	}
}

func TestDocsProblems(t *testing.T) {
	for _, tc := range []struct {
		entry Entry
		want  []string
	}{
		{Entry{Code: "SOME_CODE", Action: "Act."}, nil},
		{Entry{Code: "SOME_CODE", Action: "Act.", DocsURL: "https://docs.blaxel.ai/Sandboxes/Processes"}, nil},
		{Entry{Code: "SOME_CODE", Action: " "}, []string{"SOME_CODE has no action"}},
		{Entry{Code: "SOME_CODE", Action: "Act.", DocsURL: ReferenceURL}, []string{
			"SOME_CODE sends docs_url https://docs.blaxel.ai/troubleshooting/error-codes, which names no code anchor on the reference",
		}},
		{Entry{Code: "SOME_CODE", Action: "Act.", DocsURL: "/Sandboxes/Processes"}, []string{
			`SOME_CODE sends docs_url "/Sandboxes/Processes", which is not a page`,
		}},
	} {
		if got := docsProblems(tc.entry); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%+v: got %q, want %q", tc.entry, got, tc.want)
		}
	}
}

func TestErrorCodesReference(t *testing.T) {
	path := os.Getenv("ERROR_CODES_REFERENCE")
	if path == "" {
		t.Skip("ERROR_CODES_REFERENCE is not set: CI sets it to a checkout of the docs page")
	}
	page, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	problems := referenceDrift(string(page), referenceSection, Entries())
	for _, problem := range problems {
		t.Error(problem)
	}
	if len(problems) > 0 {
		t.Log("Regenerate the page in a blaxel-ai/docs checkout, on a branch named like this one: " +
			"node scripts/error-codes/generate.mjs --sandbox-api <sandbox>/sandbox-api/src/lib/apierrors/registry.json")
	}
}

// referenceFixture is a page with a sandbox-api block holding rows, in the
// generator's format.
func referenceFixture(rows ...string) string {
	return strings.Join(append(append([]string{
		"### Sandbox API",
		"",
		"{/* BEGIN GENERATED sandbox-api: scripts/error-codes/generate.mjs writes this table from the sandbox-api registry.json. Do not edit it by hand. */}",
		"| Code | HTTP | Retryable | Dispatch state | Message | What to do |",
		"|---|---|---|---|---|---|",
	}, rows...), "{/* END GENERATED sandbox-api */}"), "\n")
}

func TestReferenceDriftReportsEachKindOfDrift(t *testing.T) {
	entries := []Entry{
		{Code: "ARCHIVE_IN_PROGRESS", Action: "Act.", Status: 409, Retryable: true, DispatchState: NotDispatched},
		{Code: "INVALID_REQUEST_BODY", Action: "Act.", Status: 400, DispatchState: NotDispatched},
		{Code: "PROCESS_NOT_FOUND", Action: "Act.", Status: 404, DispatchState: NotDispatched},
	}
	rows := []string{
		"| <span id=\"archive-in-progress\"></span>`ARCHIVE_IN_PROGRESS` | 409 | **Yes** | `not_dispatched` | `an archive export is in progress` | Wait, then resend. |",
		// A code another service's table anchors first gets a suffixed row
		// anchor; its docs_url anchor is still on the page.
		"| <span id=\"invalid-request-body-sandbox-api\"></span>`INVALID_REQUEST_BODY` | 400 | No | `not_dispatched` | `invalid request body: {detail}` | Fix the body. |",
		"| <span id=\"process-not-found\"></span>`PROCESS_NOT_FOUND` | 404 | No | `not_dispatched` | `process not found` | List processes. |",
	}
	page := "| <span id=\"invalid-request-body\"></span>`INVALID_REQUEST_BODY` | 400 | No | `not_dispatched` | x | y |\n\n" + referenceFixture(rows...)
	expect := func(t *testing.T, page string, entries []Entry, want ...string) {
		t.Helper()
		if got := referenceDrift(page, referenceSection, entries); !reflect.DeepEqual(got, want) && (len(got) != 0 || len(want) != 0) {
			t.Errorf("drift:\n got %q\nwant %q", got, want)
		}
	}

	t.Run("in step", func(t *testing.T) {
		expect(t, page, entries)
	})
	t.Run("a registered code missing from the page", func(t *testing.T) {
		grown := append(append([]Entry{}, entries...), Entry{Code: "FILE_NOT_FOUND", Action: "Act.", Status: 404, DispatchState: NotDispatched})
		expect(t, page, grown, "FILE_NOT_FOUND is registered but missing from the page")
	})
	t.Run("a documented code no longer registered", func(t *testing.T) {
		expect(t, page, entries[:2], "PROCESS_NOT_FOUND is on the page but no longer registered")
	})
	t.Run("a changed status, retry or dispatch state", func(t *testing.T) {
		changed := append([]Entry{}, entries...)
		changed[0].Retryable = false
		changed[2].Status = 410
		changed[2].DispatchState = DispatchedUnknown
		expect(t, page, changed,
			"ARCHIVE_IN_PROGRESS: the page says retryable true, the registry false",
			"PROCESS_NOT_FOUND: the page says HTTP 404, the registry 410",
			"PROCESS_NOT_FOUND: the page says not_dispatched, the registry dispatched_unknown",
		)
	})
	t.Run("a docs_url anchor the page lacks", func(t *testing.T) {
		expect(t, referenceFixture(rows...), entries,
			"INVALID_REQUEST_BODY sends docs_url https://docs.blaxel.ai/troubleshooting/error-codes#invalid-request-body, an anchor the page does not define")
	})
	t.Run("a page without the generated block", func(t *testing.T) {
		problems := referenceDrift("## Error codes", referenceSection, entries)
		if len(problems) != 1 || !strings.Contains(problems[0], "has no single") {
			t.Errorf("got %q", problems)
		}
	})
}
