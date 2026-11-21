// Package apierrors is the sandbox API's error registry: one entry per public
// error code, with the platform error fields every error response carries.
// The fields, their names and the export format are the same as the control
// plane's error registry, so one error codes reference covers both. Every
// writer that sends an error to a client looks its code up here, so a code's
// status, action and retry fields live in one place.
//
// catalog.csv lists every error site of the sandbox API with the code it
// sends, or why it sends none; registry.json is the export the error codes
// reference is generated from. Tests keep both in step with the code.
package apierrors

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DispatchState says whether the operation the request asked for may have
// started before the error.
type DispatchState string

const (
	// NotDispatched: the request was refused before the sandbox started the
	// operation it asked for, so nothing changed.
	NotDispatched DispatchState = "not_dispatched"
	// DispatchedUnknown: the operation may have started, so some of its
	// effects may exist.
	DispatchedUnknown DispatchState = "dispatched_unknown"
)

// Origin is the origin field of every platform error.
const Origin = "platform"

// Response headers of a registered error, the same the platform gateway sets.
const (
	SourceHeader        = "X-Blaxel-Source"
	ErrorCodeHeader     = "X-Blaxel-Error-Code"
	DispatchStateHeader = "X-Blaxel-Dispatch-State"
)

// ShouldRetryHeader overrides a generated client's own retry rule. The Go SDK
// and the CLI built on it resend 408, 409, 429 and any 5xx on every method,
// POST included, unless the response says x-should-retry: false.
const ShouldRetryHeader = "X-Should-Retry"

// ReferenceURL is the public error codes reference. A code's default docs_url
// is its anchor on this page.
const ReferenceURL = "https://docs.blaxel.ai/troubleshooting/error-codes"

// Entry is one public error code.
type Entry struct {
	// Code is the stable public code, UPPER_SNAKE_CASE. Never rename one that
	// shipped: clients branch on it.
	Code string
	// Status is the HTTP status the code is sent with. It changes only with an
	// approved status code change.
	Status int
	// Message documents the message the emitting site sends, with {name} for
	// the values it fills in at runtime. The site keeps formatting its own
	// message; the reference page shows this one.
	Message string
	// Action is the one next step that works, as an instruction.
	Action string
	// DoNot is the tempting wrong move. Optional.
	DoNot string
	// Retryable: the same request may succeed later without changes.
	Retryable bool
	// DispatchState says whether the operation may have started. Read it from
	// the emitting code path; the code's owner confirms it in review.
	DispatchState DispatchState
	// DocsURL overrides the default docs_url, the code's anchor on the
	// reference page.
	DocsURL string
	// Catalog lists the catalog.csv rows the code covers.
	Catalog []string
	// NoCatalogRow replaces Catalog for a code the catalog cannot have: the
	// old path sent no error at all. One sentence on what it did instead. Set
	// exactly one of Catalog and NoCatalogRow.
	NoCatalogRow string
}

// SafeToRetryRequest is true only for a retryable error whose operation never
// started: the one case where replaying a write cannot do it twice.
func (e Entry) SafeToRetryRequest() bool {
	return e.Retryable && e.DispatchState == NotDispatched
}

// DocsAnchor is the code's anchor on the reference page.
func (e Entry) DocsAnchor() string {
	return strings.ReplaceAll(strings.ToLower(e.Code), "_", "-")
}

// ResolvedDocsURL is the docs_url the code is sent with.
func (e Entry) ResolvedDocsURL() string {
	if e.DocsURL != "" {
		return e.DocsURL
	}
	return ReferenceURL + "#" + e.DocsAnchor()
}

// placeholder matches a {name} in a message template.
var placeholder = regexp.MustCompile(`\{[a-z_]+\}`)

// MatchesMessage reports whether message is an instance of the entry's
// message template, so a test can keep a site and its entry in step.
func (e Entry) MatchesMessage(message string) bool {
	parts := placeholder.Split(e.Message, -1)
	for i, part := range parts {
		parts[i] = regexp.QuoteMeta(part)
	}
	return regexp.MustCompile("(?s)^" + strings.Join(parts, ".+") + "$").MatchString(message)
}

// Meta is what the writer knows about the failed request.
type Meta struct {
	// RequestID is the request's X-Request-Id (or the ID the sandbox API
	// generated for it), which its audit log lines carry.
	RequestID string
	Now       time.Time
}

// Fields are the platform error fields of a registered error, named as in the
// platform gateway's error body, plus error_code: the string code under the
// name every platform error body has. status is the status actually sent and
// message the client-safe message.
func (e Entry) Fields(status int, message string, meta Meta) map[string]any {
	fields := map[string]any{
		"code":                  e.Code,
		"error_code":            e.Code,
		"message":               message,
		"action":                e.Action,
		"docs_url":              e.ResolvedDocsURL(),
		"retryable":             e.Retryable,
		"safe_to_retry_request": e.SafeToRetryRequest(),
		"dispatch_state":        string(e.DispatchState),
		"status":                status,
		"origin":                Origin,
		"timestamp":             meta.Now.UTC().Format(time.RFC3339Nano),
	}
	if e.DoNot != "" {
		fields["do_not"] = e.DoNot
	}
	if meta.RequestID != "" {
		fields["request_id"] = meta.RequestID
	}
	return fields
}

// Body is the sandbox API's error body: the platform fields, and the message
// again in error, the field every sandbox API error has always had. code was
// already the string code here, so it keeps that meaning.
func (e Entry) Body(status int, message string, meta Meta) map[string]any {
	body := e.Fields(status, message, meta)
	body["error"] = message
	return body
}

// SetHeaders writes the platform error headers and the retry header.
func (e Entry) SetHeaders(set func(key, value string)) {
	set(SourceHeader, Origin)
	set(ErrorCodeHeader, e.Code)
	set(DispatchStateHeader, string(e.DispatchState))
	e.SetRetryHeader(set)
}

// SetRetryHeader sends X-Should-Retry: false when replaying the request is not
// safe (safe_to_retry_request is false), so a generated client does not
// resend it on its own. A safe error sends nothing and the client keeps its
// own rule.
func (e Entry) SetRetryHeader(set func(key, value string)) {
	if !e.SafeToRetryRequest() {
		set(ShouldRetryHeader, "false")
	}
}

// registry holds every entry by code; see registry.go.
var registry = func() map[string]Entry {
	byCode := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		if _, dup := byCode[entry.Code]; dup {
			panic(fmt.Sprintf("apierrors: duplicate code %s", entry.Code))
		}
		byCode[entry.Code] = entry
	}
	return byCode
}()

// Lookup returns the entry of code.
func Lookup(code string) (Entry, bool) {
	entry, ok := registry[code]
	return entry, ok
}

// Entries returns every entry, sorted by code.
func Entries() []Entry {
	all := make([]Entry, 0, len(registry))
	for _, entry := range registry {
		all = append(all, entry)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Code < all[j].Code })
	return all
}
