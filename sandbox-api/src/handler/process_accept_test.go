package handler

import "testing"

func TestWantsProcessStreamNegotiation(t *testing.T) {
	for _, tc := range []struct {
		accept string
		want   bool
	}{
		{"", false}, {"*/*", false}, {"application/x-ndjson", true}, {"text/event-stream", true},
		{"application/x-ndjson;q=0, application/json", false},
		{"application/x-ndjson;q=0.2, application/json;q=0.9", false},
		{"application/json;q=0.2, application/x-ndjson;q=0.9", true},
		{"application/x-ndjson;q=0, text/event-stream;q=0.5", true},
		{"application/x-ndjson-invalid", false}, {"application/x-ndjson;q=NaN", false},
		{"application/x-ndjson;q=2", false}, {"application/x-ndjson;q=bad", false},
	} {
		t.Run(tc.accept, func(t *testing.T) {
			if got := wantsProcessStream(tc.accept); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}
