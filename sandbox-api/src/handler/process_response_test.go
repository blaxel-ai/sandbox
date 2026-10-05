package handler

import (
	stdjson "encoding/json"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler/process"
)

func TestProcessResponseRequiredStrings(t *testing.T) {
	completed := time.Date(2026, 10, 5, 12, 30, 0, 0, time.UTC)
	stdout, stderr, logs := "out\n", "err\n", "out\nerr\n"
	for _, tc := range []struct {
		name     string
		snapshot process.ProcessSnapshot
		want     map[string]string
	}{
		{"unavailable", process.ProcessSnapshot{}, map[string]string{"completedAt": "", "logs": "", "stdout": "", "stderr": ""}},
		{"captured", process.ProcessSnapshot{CompletedAt: &completed, Logs: &logs, Stdout: &stdout, Stderr: &stderr}, map[string]string{"completedAt": "Mon, 05 Oct 2026 12:30:00 GMT", "logs": logs, "stdout": stdout, "stderr": stderr}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := stdjson.Marshal(processResponse(tc.snapshot))
			if err != nil {
				t.Fatal(err)
			}
			var body map[string]interface{}
			if err = stdjson.Unmarshal(raw, &body); err != nil {
				t.Fatal(err)
			}
			for key, want := range tc.want {
				got, ok := body[key].(string)
				if !ok || got != want {
					t.Errorf("%s = %#v, want string %q", key, body[key], want)
				}
			}
		})
	}
}
