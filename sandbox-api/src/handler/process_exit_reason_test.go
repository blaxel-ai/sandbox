package handler

import (
	stdjson "encoding/json"
	"strings"
	"testing"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
)

func TestProcessResponseExitFields(t *testing.T) {
	decode := func(t *testing.T, snapshot process.ProcessSnapshot) map[string]any {
		t.Helper()
		raw, err := stdjson.Marshal(processResponse(snapshot))
		if err != nil {
			t.Fatal(err)
		}
		var body map[string]any
		if err := stdjson.Unmarshal(raw, &body); err != nil {
			t.Fatal(err)
		}
		return body
	}
	for _, key := range []string{"exitReason", "oomKills", "outputTruncated", "outputLimitBytes"} {
		if _, ok := decode(t, process.ProcessSnapshot{})[key]; ok {
			t.Errorf("%s is set on a process with nothing to say", key)
		}
	}
	body := decode(t, process.ProcessSnapshot{ExitReason: process.ExitReasonOOMKilled, OOMKills: 2, OutputTruncated: true, OutputLimitBytes: 4 << 20})
	if body["exitReason"] != "oom_killed" || body["oomKills"] != float64(2) || body["outputTruncated"] != true || body["outputLimitBytes"] != float64(4<<20) {
		t.Errorf("fields = %v", body)
	}
}

// An exec whose output is longer than the response can carry says so, and
// gives the limit that applied.
func TestHandleExecuteCommandReportsTruncatedOutput(t *testing.T) {
	useTempLogDir(t)
	t.Setenv("SANDBOX_MAX_LOGS_RESPONSE_BYTES", "1024")
	h := NewProcessHandler()

	for _, tc := range []struct {
		command   string
		truncated bool
	}{
		{"yes a | head -c 5000", true},
		{"echo short", false},
	} {
		w, _ := execRequest(t, h, `{"command":"`+tc.command+`","waitForCompletion":true}`)
		var body ProcessResponse
		if err := stdjson.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: decode %q: %v", tc.command, w.Body.String(), err)
		}
		if tc.truncated {
			if !body.OutputTruncated || body.OutputLimitBytes != 1024 || len(body.Stdout) > 1024+200 || !strings.HasSuffix(body.Stdout, "a\na\n") {
				t.Errorf("%s: truncated %v, limit %d, %d bytes of stdout; want true, 1024, the last 1024 bytes",
					tc.command, body.OutputTruncated, body.OutputLimitBytes, len(body.Stdout))
			}
		} else if body.OutputTruncated || body.OutputLimitBytes != 0 || body.Stdout != "short\n" {
			t.Errorf("%s: truncated %v, limit %d, stdout %q; want the whole output, unflagged",
				tc.command, body.OutputTruncated, body.OutputLimitBytes, body.Stdout)
		}
	}
}
