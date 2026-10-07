package handler

import (
	"encoding/base64"
	"mime"
	"strconv"
	"strings"
	"unicode/utf8"
)

// wantsLogNDJSON requires an explicit opt-in; wildcards and the historical
// text/event-stream Accept value keep the default plain-text representation.
func wantsLogNDJSON(accept string) bool {
	ndjsonQuality, textQuality := 0.0, 0.0
	for _, mediaRange := range strings.Split(accept, ",") {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(mediaRange))
		if err != nil {
			continue
		}
		quality := 1.0
		if raw, ok := params["q"]; ok {
			quality, err = strconv.ParseFloat(raw, 64)
			if err != nil || !(quality >= 0 && quality <= 1) {
				continue
			}
		}
		switch mediaType {
		case "application/x-ndjson":
			ndjsonQuality = max(ndjsonQuality, quality)
		case "text/plain":
			textQuality = max(textQuality, quality)
		}
	}
	return ndjsonQuality > 0 && ndjsonQuality >= textQuality
}

// ProcessLogEvent is one newline-delimited JSON record in a log stream.
type ProcessLogEvent struct {
	Type string `json:"type" example:"stdout"`
	Data string `json:"data,omitempty" example:"Name?"`
	// Set to base64 when data carries bytes that are not a complete UTF-8 string.
	Encoding string `json:"encoding,omitempty" example:"base64"`
}

type logJSONStreamWriter struct {
	*ResponseWriter
}

func (w *logJSONStreamWriter) IsJSONStreamWriter() bool    { return true }
func (w *logJSONStreamWriter) RequireStructuredLogs() bool { return true }

func (w *logJSONStreamWriter) WriteEvent(eventType, data string) (int, error) {
	event := ProcessLogEvent{Type: eventType, Data: data}
	// A read can split a UTF-8 code point, and process output may be binary.
	// Preserve those bytes instead of silently replacing them with U+FFFD.
	if !utf8.ValidString(data) {
		event.Data = base64.StdEncoding.EncodeToString([]byte(data))
		event.Encoding = "base64"
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return 0, err
	}
	return w.ResponseWriter.Write(append(encoded, '\n'))
}

func (w *logJSONStreamWriter) Write(data []byte) (int, error) {
	if _, err := w.WriteEvent("stdout", string(data)); err != nil {
		return 0, err
	}
	return len(data), nil
}
