package handler

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
	"github.com/blaxel-ai/sandbox-api/src/lib/apierrors"
	"github.com/blaxel-ai/sandbox-api/src/lib/audit"
)

// platformKeys are the fields every error carries, in a response body and in
// a stream error event.
var platformKeys = []string{"code", "error_code", "message", "action", "docs_url", "retryable", "safe_to_retry_request", "dispatch_state", "status", "origin", "timestamp"}

// checkErrorFields checks the platform error fields of a decoded error body or
// stream error event against entry.
func checkErrorFields(t *testing.T, fields map[string]any, entry apierrors.Entry, status int, message string) {
	t.Helper()
	for _, key := range platformKeys {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing %s in %v", key, fields)
		}
	}
	if fields["code"] != entry.Code || fields["error_code"] != entry.Code {
		t.Errorf("code %v, error_code %v: want %s", fields["code"], fields["error_code"], entry.Code)
	}
	if fields["message"] != message {
		t.Errorf("message %q, want %q", fields["message"], message)
	}
	if fields["retryable"] != entry.Retryable || fields["safe_to_retry_request"] != entry.SafeToRetryRequest() || fields["dispatch_state"] != string(entry.DispatchState) {
		t.Errorf("retryable %v, safe_to_retry_request %v, dispatch_state %v: want %v, %v, %s",
			fields["retryable"], fields["safe_to_retry_request"], fields["dispatch_state"], entry.Retryable, entry.SafeToRetryRequest(), entry.DispatchState)
	}
	if fields["status"] != float64(status) {
		t.Errorf("status %v, want %d", fields["status"], status)
	}
	if fields["origin"] != "platform" || fields["action"] != entry.Action || fields["docs_url"] != entry.ResolvedDocsURL() {
		t.Errorf("origin %v, action %v, docs_url %v", fields["origin"], fields["action"], fields["docs_url"])
	}
	if (entry.DoNot != "") != (fields["do_not"] != nil) {
		t.Errorf("do_not %v, want %q", fields["do_not"], entry.DoNot)
	}
	timestamp, _ := fields["timestamp"].(string)
	if when, err := time.Parse(time.RFC3339Nano, timestamp); err != nil || when.Location() != time.UTC {
		t.Errorf("timestamp %q is not RFC 3339 in UTC", timestamp)
	}
}

func errorContext(requestID string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/process", nil)
	if requestID != "" {
		ctx.Set(audit.ContextKeyRequestID, requestID)
	}
	return ctx, recorder
}

func TestSendErrorCarriesThePlatformFields(t *testing.T) {
	ctx, recorder := errorContext("req-1")
	err := errors.New("process with name 'web' already exists and is running")
	NewBaseHandler().SendError(ctx, apierrors.ProcessNameInUse, err)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want the 400 it always had", recorder.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != err.Error() {
		t.Errorf("error %v, want the message unchanged for existing clients", body["error"])
	}
	checkErrorFields(t, body, apierrors.ProcessNameInUse, http.StatusBadRequest, err.Error())
	if body["request_id"] != "req-1" {
		t.Errorf("request_id %v, want req-1", body["request_id"])
	}
	for header, want := range map[string]string{
		"X-Blaxel-Source":         "platform",
		"X-Blaxel-Error-Code":     "PROCESS_NAME_IN_USE",
		"X-Blaxel-Dispatch-State": "not_dispatched",
		"X-Should-Retry":          "false",
	} {
		if got := recorder.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestSendErrorLeavesRetryToTheClientWhenReplayIsSafe(t *testing.T) {
	ctx, recorder := errorContext("")
	NewBaseHandler().SendError(ctx, apierrors.ArchiveInProgress, errors.New("an archive export is in progress"))

	if got := recorder.Header().Values("X-Should-Retry"); len(got) != 0 {
		t.Errorf("X-Should-Retry = %v on an error that is safe to resend", got)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	checkErrorFields(t, body, apierrors.ArchiveInProgress, http.StatusConflict, "an archive export is in progress")
	if _, ok := body["request_id"]; ok {
		t.Errorf("request_id %v sent without a request ID", body["request_id"])
	}
}

func TestStreamErrorEventCarriesThePlatformFields(t *testing.T) {
	ctx, recorder := errorContext("req-2")
	ctx.Writer.WriteHeader(http.StatusOK) // the stream answered 200 before it failed
	jw := &JSONStreamWriter{gin: ctx}
	if _, err := jw.SendError(apierrors.ProcessStreamFailed, errors.New("broken pipe")); err != nil {
		t.Fatal(err)
	}

	line := strings.TrimSpace(recorder.Body.String())
	var event map[string]any
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	if event["type"] != "error" || event["data"] != "broken pipe" {
		t.Errorf("event %s: want type error and the message in data, as before", line)
	}
	// status is the one the same error gets as a response, not the stream's 200.
	checkErrorFields(t, event, apierrors.ProcessStreamFailed, apierrors.ProcessStreamFailed.Status, "broken pipe")
	if event["request_id"] != "req-2" {
		t.Errorf("request_id %v, want req-2", event["request_id"])
	}
}

func TestLogStreamErrorEventCarriesThePlatformFields(t *testing.T) {
	ctx, recorder := errorContext("")
	w := &logJSONStreamWriter{ResponseWriter: &ResponseWriter{gin: ctx}}
	if _, err := w.SendError(apierrors.ProcessLogStreamFailed, errors.New("log file vanished")); err != nil {
		t.Fatal(err)
	}

	line := strings.TrimSpace(recorder.Body.String())
	var event map[string]any
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("decode %q: %v", line, err)
	}
	if event["type"] != "error" || event["data"] != "log file vanished" {
		t.Errorf("event %s", line)
	}
	checkErrorFields(t, event, apierrors.ProcessLogStreamFailed, apierrors.ProcessLogStreamFailed.Status, "log file vanished")
}

func TestExecErrorEntrySaysWhetherTheCommandRan(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want apierrors.Entry
	}{
		{"keepAlive outside the plan", ErrKeepAliveDisabled, apierrors.KeepAliveUnavailable},
		{"start failed", &process.StartError{Err: errors.New("exec: \"nope\": executable file not found in $PATH")}, apierrors.ProcessStartFailed},
		{"ports wait ran out", fmt.Errorf("%w after 5 seconds", process.ErrPortsWaitExpired), apierrors.ProcessPortsTimeout},
		{"completion wait ran out", fmt.Errorf("%w after 5 seconds", process.ErrCompletionWaitExpired), apierrors.ProcessTimedOut},
		{"anything else", errors.New("process creation failed because process does not exist"), apierrors.ProcessExecFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry, err := execErrorEntry(tc.err)
			if entry.Code != tc.want.Code {
				t.Errorf("entry %s, want %s", entry.Code, tc.want.Code)
			}
			if err.Error() != tc.err.Error() {
				t.Errorf("message %q changed from %q", err, tc.err)
			}
		})
	}
}

func TestSignalAndStdinErrorEntries(t *testing.T) {
	for _, tc := range []struct {
		got, want apierrors.Entry
	}{
		{signalErrorEntry(fmt.Errorf("%w: web", process.ErrProcessNotFound)), apierrors.ProcessNotFound},
		{signalErrorEntry(fmt.Errorf("%w: web", process.ErrProcessNotRunning)), apierrors.ProcessNotRunning},
		{signalErrorEntry(errors.New("failed to send SIGTERM to process web: operation not permitted")), apierrors.ProcessSignalFailed},
		{stdinErrorEntry(process.ErrStdinNotEnabled), apierrors.ProcessStdinNotEnabled},
		{stdinErrorEntry(process.ErrStdinClosed), apierrors.ProcessStdinClosed},
		{stdinErrorEntry(process.ErrStdinStalled), apierrors.ProcessStdinStalled},
		{stdinErrorEntry(errors.New("process with Identifier web not found")), apierrors.ProcessNotFound},
		{stdinErrorEntry(errors.New("broken pipe")), apierrors.ProcessStdinFailed},
	} {
		if tc.got.Code != tc.want.Code {
			t.Errorf("got %s, want %s", tc.got.Code, tc.want.Code)
		}
	}
}
