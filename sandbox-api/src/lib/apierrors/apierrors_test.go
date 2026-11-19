package apierrors

import (
	"net/http"
	"os"
	"reflect"
	"regexp"
	"testing"
	"time"
)

var (
	codePattern    = regexp.MustCompile(`^[A-Z][A-Z0-9]*(_[A-Z0-9]+)*$`)
	catalogPattern = regexp.MustCompile(`^S\d{4}$`)
)

func TestEveryEntryIsComplete(t *testing.T) {
	if len(Entries()) == 0 {
		t.Fatal("no entries")
	}
	anchors := map[string]string{}
	for _, entry := range Entries() {
		t.Run(entry.Code, func(t *testing.T) {
			if !codePattern.MatchString(entry.Code) {
				t.Errorf("code %q is not UPPER_SNAKE_CASE", entry.Code)
			}
			if entry.Status < 400 || entry.Status > 599 {
				t.Errorf("status %d is not an error status", entry.Status)
			}
			if entry.Message == "" || entry.Action == "" {
				t.Error("message and action are required")
			}
			if entry.DispatchState != NotDispatched && entry.DispatchState != DispatchedUnknown {
				t.Errorf("dispatch state %q", entry.DispatchState)
			}
			if problem := catalogTrace(entry); problem != "" {
				t.Error(problem)
			}
			for _, row := range entry.Catalog {
				if !catalogPattern.MatchString(row) {
					t.Errorf("catalog row %q", row)
				}
			}
			if other, taken := anchors[entry.DocsAnchor()]; taken {
				t.Errorf("docs anchor shared with %s", other)
			}
			anchors[entry.DocsAnchor()] = entry.Code
			if found, ok := Lookup(entry.Code); !ok || found.Code != entry.Code {
				t.Error("Lookup does not find the entry")
			}
		})
	}
}

// catalogTrace says what is wrong with how an entry traces to catalog.csv:
// its rows in Catalog, or NoCatalogRow when the old path sent no error at
// all. Empty when it is right.
func catalogTrace(entry Entry) string {
	switch {
	case len(entry.Catalog) == 0 && entry.NoCatalogRow == "":
		return "set Catalog to the code's catalog rows, or NoCatalogRow when the old path sent no error at all"
	case len(entry.Catalog) > 0 && entry.NoCatalogRow != "":
		return "set Catalog or NoCatalogRow, not both"
	}
	return ""
}

func TestCatalogTrace(t *testing.T) {
	for _, test := range []struct {
		entry Entry
		ok    bool
	}{
		{Entry{Catalog: []string{"S0000"}}, true},
		{Entry{NoCatalogRow: "answered 200"}, true},
		{Entry{}, false},
		{Entry{Catalog: []string{"S0000"}, NoCatalogRow: "answered 200"}, false},
	} {
		if got := catalogTrace(test.entry) == ""; got != test.ok {
			t.Errorf("catalogTrace(%+v) ok = %v, want %v", test.entry, got, test.ok)
		}
	}
}

// The exec codes the sandbox API had before the registry keep their fields.
func TestSeedCodes(t *testing.T) {
	for _, test := range []struct {
		entry     Entry
		retryable bool
		state     DispatchState
	}{
		{ProcessWaitLimitReached, false, DispatchedUnknown},
		{ProcessWorkingDirUnusable, false, NotDispatched},
	} {
		if test.entry.Status != http.StatusUnprocessableEntity || test.entry.Retryable != test.retryable ||
			test.entry.DispatchState != test.state || test.entry.SafeToRetryRequest() {
			t.Errorf("%s: %+v", test.entry.Code, test.entry)
		}
	}
}

func TestSafeToRetryRequestNeedsARetryableErrorThatNeverStarted(t *testing.T) {
	for _, test := range []struct {
		retryable bool
		state     DispatchState
		safe      bool
	}{
		{true, NotDispatched, true},
		{true, DispatchedUnknown, false},
		{false, NotDispatched, false},
		{false, DispatchedUnknown, false},
	} {
		entry := Entry{Retryable: test.retryable, DispatchState: test.state}
		if got := entry.SafeToRetryRequest(); got != test.safe {
			t.Errorf("%+v: safe = %v", test, got)
		}
	}
}

func TestBodyKeepsTheErrorField(t *testing.T) {
	meta := Meta{RequestID: "4bf92f35-77b3-4da6-a3ce-929d0e0e4736", Now: time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)}
	message := "process with name 'web' already exists and is running"
	want := map[string]any{
		"error":                 message,
		"code":                  "PROCESS_NAME_IN_USE",
		"error_code":            "PROCESS_NAME_IN_USE",
		"message":               message,
		"action":                ProcessNameInUse.Action,
		"do_not":                ProcessNameInUse.DoNot,
		"docs_url":              "https://docs.blaxel.ai/troubleshooting/error-codes#process-name-in-use",
		"retryable":             false,
		"safe_to_retry_request": false,
		"dispatch_state":        "not_dispatched",
		"status":                http.StatusBadRequest,
		"origin":                "platform",
		"timestamp":             "2026-11-18T12:00:00Z",
		"request_id":            meta.RequestID,
	}
	if got := ProcessNameInUse.Body(http.StatusBadRequest, message, meta); !reflect.DeepEqual(got, want) {
		t.Errorf("Body = %#v\nwant %#v", got, want)
	}

	fields := ArchiveInProgress.Fields(http.StatusConflict, "busy", Meta{Now: meta.Now})
	if _, ok := fields["request_id"]; ok {
		t.Error("request_id without a request ID")
	}
	if _, ok := fields["do_not"]; ok {
		t.Error("do_not without a DoNot")
	}
	if fields["retryable"] != true || fields["safe_to_retry_request"] != true {
		t.Errorf("retry fields %v %v", fields["retryable"], fields["safe_to_retry_request"])
	}
}

func TestSetHeaders(t *testing.T) {
	headers := map[string]string{}
	ProcessWaitLimitReached.SetHeaders(func(k, v string) { headers[k] = v })
	want := map[string]string{
		"X-Blaxel-Source":         "platform",
		"X-Blaxel-Error-Code":     "PROCESS_WAIT_LIMIT_REACHED",
		"X-Blaxel-Dispatch-State": "dispatched_unknown",
		"X-Should-Retry":          "false",
	}
	if !reflect.DeepEqual(headers, want) {
		t.Errorf("headers = %v", headers)
	}
}

func TestSetRetryHeaderOnlyWhenReplayIsUnsafe(t *testing.T) {
	headers := func(e Entry) map[string]string {
		got := map[string]string{}
		e.SetRetryHeader(func(k, v string) { got[k] = v })
		return got
	}
	// Possibly dispatched: replaying could run the command twice.
	if got := headers(ProcessWaitLimitReached); got["X-Should-Retry"] != "false" {
		t.Errorf("wait limit: %v", got)
	}
	// Not retryable at all.
	if got := headers(InvalidRequestBody); got["X-Should-Retry"] != "false" {
		t.Errorf("invalid body: %v", got)
	}
	// Safe to replay: the client keeps its own rule.
	if got := headers(ArchiveInProgress); len(got) != 0 {
		t.Errorf("archive in progress: %v", got)
	}
}

func TestMatchesMessage(t *testing.T) {
	if !ProcessNameInUse.MatchesMessage("process with name 'web' already exists and is running") {
		t.Error("name in use")
	}
	if ProcessNameInUse.MatchesMessage("process with name 'web' already exists") {
		t.Error("partial message matched")
	}
	if !ProcessTimedOut.MatchesMessage("process timed out after 30 seconds") {
		t.Error("timed out")
	}
}

// registry.json is the export other tools read (the error codes reference and
// its drift check). Regenerate it with UPDATE_EXPORT=1 go test ./src/lib/apierrors.
func TestExportUpToDate(t *testing.T) {
	got, err := ExportJSON()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_EXPORT") == "1" {
		if err := os.WriteFile("registry.json", got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("registry.json")
	if err != nil {
		t.Fatalf("%v: run UPDATE_EXPORT=1 go test ./src/lib/apierrors", err)
	}
	if string(want) != string(got) {
		t.Error("registry.json is stale: run UPDATE_EXPORT=1 go test ./src/lib/apierrors")
	}
}
