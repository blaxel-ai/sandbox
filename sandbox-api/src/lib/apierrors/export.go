package apierrors

import (
	"bytes"
	"encoding/json"
)

// ExportVersion is the version of the export format.
const ExportVersion = 1

// ExportSource names the service the codes come from in the export.
const ExportSource = "sandbox-api"

// ExportedCode is one code in the export, in the field names of the error
// body.
type ExportedCode struct {
	Code               string   `json:"code"`
	Status             int      `json:"status"`
	Message            string   `json:"message"`
	Action             string   `json:"action"`
	DoNot              string   `json:"do_not,omitempty"`
	Retryable          bool     `json:"retryable"`
	SafeToRetryRequest bool     `json:"safe_to_retry_request"`
	DispatchState      string   `json:"dispatch_state"`
	DocsURL            string   `json:"docs_url"`
	DocsAnchor         string   `json:"docs_anchor"`
	Catalog            []string `json:"catalog,omitempty"`
}

// Export is the registry as the error codes reference and its drift check
// read it (registry.json, kept in step by TestExportUpToDate).
type Export struct {
	Version int            `json:"version"`
	Source  string         `json:"source"`
	Codes   []ExportedCode `json:"codes"`
}

// ExportJSON renders the registry export, sorted by code.
func ExportJSON() ([]byte, error) {
	export := Export{Version: ExportVersion, Source: ExportSource}
	for _, entry := range Entries() {
		export.Codes = append(export.Codes, ExportedCode{
			Code:               entry.Code,
			Status:             entry.Status,
			Message:            entry.Message,
			Action:             entry.Action,
			DoNot:              entry.DoNot,
			Retryable:          entry.Retryable,
			SafeToRetryRequest: entry.SafeToRetryRequest(),
			DispatchState:      string(entry.DispatchState),
			DocsURL:            entry.ResolvedDocsURL(),
			DocsAnchor:         entry.DocsAnchor(),
			Catalog:            entry.Catalog,
		})
	}
	// Messages and actions hold <placeholders>; keep them readable.
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(export); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
