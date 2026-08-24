package process

import (
	"strings"
	"testing"
)

// The read buffer is a fixed-size window, not a line. Prefixing the chunk
// rather than its line starts spliced "stdout:" into the middle of any line
// longer than the buffer, which corrupted it for every consumer: a >4KB JSON
// line arrived with the tag at byte 4096 and stopped parsing.
func TestPrefixLines(t *testing.T) {
	tests := []struct {
		name            string
		chunks          []string
		wantOut         []string
		wantReassembled string
	}{
		{
			name:            "whole lines are each tagged",
			chunks:          []string{"a\nb\n"},
			wantOut:         []string{"stdout:a\nstdout:b\n"},
			wantReassembled: "stdout:a\nstdout:b\n",
		},
		{
			name:            "a line split across chunks is tagged once",
			chunks:          []string{"{\"big\":\"aaa", "bbb", "ccc\"}\n"},
			wantOut:         []string{"stdout:{\"big\":\"aaa", "bbb", "ccc\"}\n"},
			wantReassembled: "stdout:{\"big\":\"aaabbbccc\"}\n",
		},
		{
			name:            "a chunk ending exactly on a newline starts the next one fresh",
			chunks:          []string{"first\n", "second\n"},
			wantOut:         []string{"stdout:first\n", "stdout:second\n"},
			wantReassembled: "stdout:first\nstdout:second\n",
		},
		{
			name:            "trailing partial line resumes untagged",
			chunks:          []string{"done\npart", "ial\n"},
			wantOut:         []string{"stdout:done\nstdout:part", "ial\n"},
			wantReassembled: "stdout:done\nstdout:partial\n",
		},
		{
			name:            "empty chunk changes nothing",
			chunks:          []string{"x\n", "", "y\n"},
			wantOut:         []string{"stdout:x\n", "", "stdout:y\n"},
			wantReassembled: "stdout:x\nstdout:y\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			atLineStart := true
			var got []string
			var all strings.Builder
			for _, chunk := range tc.chunks {
				out, next := prefixLines("stdout", []byte(chunk), atLineStart)
				atLineStart = next
				got = append(got, string(out))
				all.Write(out)
			}
			for i := range tc.wantOut {
				if got[i] != tc.wantOut[i] {
					t.Errorf("chunk %d: got %q, want %q", i, got[i], tc.wantOut[i])
				}
			}
			if all.String() != tc.wantReassembled {
				t.Errorf("reassembled: got %q, want %q", all.String(), tc.wantReassembled)
			}
		})
	}
}

// The regression in its original shape: one line longer than the read buffer.
func TestPrefixLinesLeavesLongLineIntact(t *testing.T) {
	const bufSize = 4096
	payload := strings.Repeat("x", 9000)
	line := `{"type":"tool","output":"` + payload + `"}` + "\n"

	atLineStart := true
	var wire strings.Builder
	for i := 0; i < len(line); i += bufSize {
		end := i + bufSize
		if end > len(line) {
			end = len(line)
		}
		out, next := prefixLines("stdout", []byte(line[i:end]), atLineStart)
		atLineStart = next
		wire.Write(out)
	}

	got := wire.String()
	if strings.Count(got, "stdout:") != 1 {
		t.Fatalf("expected exactly one tag, got %d", strings.Count(got, "stdout:"))
	}
	if body := strings.TrimPrefix(got, "stdout:"); body != line {
		t.Errorf("line was altered in transit:\n got %.80q...\nwant %.80q...", body, line)
	}
}
