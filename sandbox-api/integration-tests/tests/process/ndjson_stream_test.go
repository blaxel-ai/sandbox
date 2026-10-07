package tests

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/require"
)

type ndjsonLogRecord struct {
	Type     string `json:"type"`
	Data     string `json:"data"`
	Encoding string `json:"encoding,omitempty"`
}

func TestNDJSONLogStreamOpensBeforeStdin(t *testing.T) {
	name := uniqueProcessName("ndjson-quiet-open")
	response, err := common.MakeRequest(http.MethodPost, "/process", map[string]any{
		"name": name, "command": "read gate; printf released", "stdin": true, "keepAlive": true,
	})
	require.NoError(t, err)
	response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)
	t.Cleanup(func() {
		if r, err := common.MakeRequest(http.MethodDelete, "/process/"+name+"/kill", nil); err == nil {
			r.Body.Close()
		}
	})

	// Send no stdin until both the headers and the opening record arrive. This
	// also detects intermediaries that withhold headers until the first body byte.
	stream := openLogStream(t, name, "application/x-ndjson")
	requireLogMediaType(t, stream, "application/x-ndjson")
	reader := bufio.NewReader(stream.Body)
	line, err := reader.ReadBytes('\n')
	require.NoError(t, err, "quiet stream must open before stdin")
	var opening ndjsonLogRecord
	require.NoError(t, json.Unmarshal(line, &opening))
	require.Equal(t, ndjsonLogRecord{Type: "keepalive"}, opening)
	require.Equal(t, http.StatusOK, writeStdin(t, name, "go").StatusCode)
	require.Equal(t, map[string]string{"stdout": "released", "stderr": ""}, collectNDJSONLogs(t, reader))
}

func openLogStream(t *testing.T, name, accept string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, common.BaseURL+"/process/"+name+"/logs/stream", nil)
	require.NoError(t, err)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func requireLogMediaType(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	require.NoError(t, err)
	require.Equal(t, want, mediaType)
}

// Decode one physical line at a time: an output newline must be escaped inside
// its record rather than terminate the JSON document or create an empty line.
func nextNDJSONLog(t *testing.T, reader *bufio.Reader) (ndjsonLogRecord, bool) {
	t.Helper()
	for {
		line, err := reader.ReadBytes('\n')
		if err == io.EOF && len(line) == 0 {
			return ndjsonLogRecord{}, false
		}
		require.NoError(t, err, "stream must close between complete NDJSON records")
		var record ndjsonLogRecord
		require.NoError(t, json.Unmarshal(line, &record), "invalid NDJSON record: %q", line)
		switch record.Type {
		case "keepalive":
			require.Empty(t, record.Data, "keepalive must not become process output")
		case "stdout", "stderr":
			switch record.Encoding {
			case "":
			case "base64":
				data, err := base64.StdEncoding.DecodeString(record.Data)
				require.NoError(t, err)
				record.Data = string(data)
			default:
				t.Fatalf("unknown output encoding %q", record.Encoding)
			}
			return record, true
		default:
			t.Fatalf("unexpected record type %q: %s", record.Type, record.Data)
		}
	}
}

func readNDJSONSource(t *testing.T, reader *bufio.Reader, source, expected string) {
	t.Helper()
	var got strings.Builder
	for got.Len() < len(expected) {
		record, ok := nextNDJSONLog(t, reader)
		require.True(t, ok, "stream ended before %s data arrived", source)
		require.Equal(t, source, record.Type, "source ordering changed")
		got.WriteString(record.Data)
	}
	require.Equal(t, expected, got.String())
}

func collectNDJSONLogs(t *testing.T, reader *bufio.Reader) map[string]string {
	t.Helper()
	output := map[string]string{"stdout": "", "stderr": ""}
	for {
		record, ok := nextNDJSONLog(t, reader)
		if !ok {
			return output
		}
		output[record.Type] += record.Data
	}
}

func startCompletedLogProcess(t *testing.T, command string) string {
	t.Helper()
	name := uniqueProcessName("ndjson-backlog")
	resp, err := common.MakeRequest(http.MethodPost, "/process", map[string]any{
		"name": name, "command": command, "waitForCompletion": true,
	})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var process map[string]any
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&process))
	require.Equal(t, "completed", process["status"])
	return name
}

func TestNDJSONLogStreamPreservesLivePartialOutputAndReplay(t *testing.T) {
	name := uniqueProcessName("ndjson-gated")
	// Each write waits for the client to acknowledge the previous record. This
	// proves partial output is delivered immediately without sleep-based ordering.
	startWithStdin(t, name, `read gate; printf 'Name? '; read gate; printf 'oops\n' >&2; read gate; printf 'Bob\n'`)
	resp := openLogStream(t, name, "application/x-ndjson")
	requireLogMediaType(t, resp, "application/x-ndjson")
	reader := bufio.NewReader(resp.Body)
	require.Equal(t, http.StatusOK, writeStdin(t, name, "first").StatusCode)
	readNDJSONSource(t, reader, "stdout", "Name? ")
	require.Equal(t, http.StatusOK, writeStdin(t, name, "second").StatusCode)
	readNDJSONSource(t, reader, "stderr", "oops\n")
	require.Equal(t, http.StatusOK, writeStdin(t, name, "third").StatusCode)
	readNDJSONSource(t, reader, "stdout", "Bob\n")
	_, ok := nextNDJSONLog(t, reader)
	require.False(t, ok, "finished log stream must close without a result event")
	resp.Body.Close()

	replay := openLogStream(t, name, "application/x-ndjson")
	requireLogMediaType(t, replay, "application/x-ndjson")
	require.Equal(t, map[string]string{"stdout": "Name? Bob\n", "stderr": "oops\n"}, collectNDJSONLogs(t, bufio.NewReader(replay.Body)))
}

func TestNDJSONLogStreamPreservesEscapesPrefixesAndLongOutput(t *testing.T) {
	const prefix = "stdout:literal\nstderr:literal\n[keepalive]\n{\"type\":\"error\",\"data\":\"literal\"}\n\t\r\\\"\n"
	// POSIX shell tools are available in both integration identity modes.
	name := startCompletedLogProcess(t, `printf 'stdout:literal\nstderr:literal\n[keepalive]\n{"type":"error","data":"literal"}\n\t\r\\"\n'; head -c 131072 /dev/zero | tr '\000' x; printf 'stderr:tail' >&2`)
	resp := openLogStream(t, name, "application/x-ndjson")
	requireLogMediaType(t, resp, "application/x-ndjson")
	output := collectNDJSONLogs(t, bufio.NewReader(resp.Body))
	require.Equal(t, prefix+strings.Repeat("x", 131072), output["stdout"])
	require.Equal(t, "stderr:tail", output["stderr"])
}

func TestNDJSONLogStreamAcceptNegotiation(t *testing.T) {
	name := startCompletedLogProcess(t, `printf 'hello\n'`)
	for _, tc := range []struct{ name, accept, mediaType string }{
		{"default", "", "text/plain"},
		{"text", "text/plain", "text/plain"},
		{"wildcard", "*/*", "text/plain"},
		{"ndjson", "application/x-ndjson", "application/x-ndjson"},
		{"list", "text/plain, application/x-ndjson", "application/x-ndjson"},
		{"disabled", "application/x-ndjson;q=0, text/plain", "text/plain"},
		{"prefer-text", "application/x-ndjson;q=0.5, text/plain;q=0.9", "text/plain"},
		{"prefer-ndjson", "text/plain;q=0.5, application/x-ndjson;q=0.9", "application/x-ndjson"},
		{"application-wildcard", "application/*", "text/plain"},
		{"unsupported", "application/xml", "text/plain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := openLogStream(t, name, tc.accept)
			requireLogMediaType(t, resp, tc.mediaType)
			if tc.mediaType == "application/x-ndjson" {
				require.Equal(t, map[string]string{"stdout": "hello\n", "stderr": ""}, collectNDJSONLogs(t, bufio.NewReader(resp.Body)))
			} else {
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				require.Equal(t, "stdout:hello\n", string(body), "legacy text framing changed")
			}
		})
	}
}

func TestNDJSONLogStreamMissingProcessIs404(t *testing.T) {
	resp := openLogStream(t, uniqueProcessName("ndjson-missing"), "application/x-ndjson")
	require.Equal(t, http.StatusNotFound, resp.StatusCode, "lookup errors must be reported before stream headers are committed")
}

func TestNDJSONLogStreamEmptyProcessCloses(t *testing.T) {
	name := startCompletedLogProcess(t, "true")
	resp := openLogStream(t, name, "application/x-ndjson")
	requireLogMediaType(t, resp, "application/x-ndjson")
	require.Equal(t, map[string]string{"stdout": "", "stderr": ""}, collectNDJSONLogs(t, bufio.NewReader(resp.Body)))
}

func TestNDJSONLogStreamDisconnectKeepsProcessRunning(t *testing.T) {
	name := uniqueProcessName("ndjson-disconnect")
	startWithStdin(t, name, `read gate; printf 'first'; read gate; printf 'second'`)
	resp := openLogStream(t, name, "application/x-ndjson")
	requireLogMediaType(t, resp, "application/x-ndjson")
	require.Equal(t, http.StatusOK, writeStdin(t, name, "first").StatusCode)
	readNDJSONSource(t, bufio.NewReader(resp.Body), "stdout", "first")
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, writeStdin(t, name, "second").StatusCode)
	replay := openLogStream(t, name, "application/x-ndjson")
	requireLogMediaType(t, replay, "application/x-ndjson")
	require.Equal(t, map[string]string{"stdout": "firstsecond", "stderr": ""}, collectNDJSONLogs(t, bufio.NewReader(replay.Body)))
}

func TestNDJSONLogStreamPreservesBinaryAndSplitUTF8(t *testing.T) {
	name := uniqueProcessName("ndjson-binary")
	// Split a UTF-8 code point across acknowledged writes. Each individual
	// record needs base64, but concatenated source bytes must remain unchanged.
	startWithStdin(t, name, `read gate; printf '\377\000\342'; read gate; printf '\202\254\n'`)
	resp := openLogStream(t, name, "application/x-ndjson")
	requireLogMediaType(t, resp, "application/x-ndjson")
	reader := bufio.NewReader(resp.Body)
	require.Equal(t, http.StatusOK, writeStdin(t, name, "first").StatusCode)
	readNDJSONSource(t, reader, "stdout", string([]byte{0xff, 0, 0xe2}))
	require.Equal(t, http.StatusOK, writeStdin(t, name, "second").StatusCode)
	readNDJSONSource(t, reader, "stdout", string([]byte{0x82, 0xac, '\n'}))
	_, ok := nextNDJSONLog(t, reader)
	require.False(t, ok)
	replay := openLogStream(t, name, "application/x-ndjson")
	requireLogMediaType(t, replay, "application/x-ndjson")
	require.Equal(t, map[string]string{"stdout": string([]byte{0xff, 0, 0xe2, 0x82, 0xac, '\n'}), "stderr": ""}, collectNDJSONLogs(t, bufio.NewReader(replay.Body)))
}

func TestNDJSONLogStreamRestartIsControlNotStdout(t *testing.T) {
	name := uniqueProcessName("ndjson-restart")
	marker := "/tmp/" + name + ".once"
	// The first run cannot exit until the stream is attached. The second run
	// removes its marker and finishes, so the test observes one automatic restart.
	command := "if [ ! -f " + marker + " ]; then touch " + marker + "; read gate; printf 'first'; exit 1; else rm " + marker + "; printf 'second\\n'; fi"
	resp, err := common.MakeRequest(http.MethodPost, "/process", map[string]any{
		"name": name, "command": command, "stdin": true, "restartOnFailure": true, "maxRestarts": 1,
	})
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	t.Cleanup(func() {
		if r, err := common.MakeRequest(http.MethodDelete, "/process/"+name+"/kill", nil); err == nil {
			r.Body.Close()
		}
	})
	stream := openLogStream(t, name, "application/x-ndjson")
	requireLogMediaType(t, stream, "application/x-ndjson")
	require.Equal(t, http.StatusOK, writeStdin(t, name, "continue").StatusCode)

	readRuns := func(resp *http.Response) map[string]string {
		reader := bufio.NewReader(resp.Body)
		output := map[string]string{"stdout": "", "stderr": ""}
		notices := 0
		for {
			line, err := reader.ReadBytes('\n')
			if err == io.EOF && len(line) == 0 {
				break
			}
			require.NoError(t, err)
			var record ndjsonLogRecord
			require.NoError(t, json.Unmarshal(line, &record))
			switch record.Type {
			case "stdout", "stderr":
				require.Empty(t, record.Encoding)
				output[record.Type] += record.Data
			case "restart":
				notices++
				require.Contains(t, record.Data, "Attempting restart")
				require.Equal(t, "first", output["stdout"], "restart must follow the drained first run and precede the second")
			case "keepalive":
				require.Empty(t, record.Data)
			default:
				t.Fatalf("unexpected record %q: %s", record.Type, record.Data)
			}
		}
		require.Equal(t, 1, notices, "restart must be reported exactly once as control data")
		require.Equal(t, map[string]string{"stdout": "firstsecond\n", "stderr": ""}, output)
		return output
	}
	liveOutput := readRuns(stream)
	replay := openLogStream(t, name, "application/x-ndjson")
	requireLogMediaType(t, replay, "application/x-ndjson")
	require.Equal(t, liveOutput, readRuns(replay))
}

func TestNDJSONLogStreamSameNameProcessesKeepIndependentLogs(t *testing.T) {
	name := uniqueProcessName("ndjson-shared-name")
	start := func(command string, stdin bool) string {
		response, err := common.MakeRequest(http.MethodPost, "/process", map[string]any{
			"name": name, "command": command, "stdin": stdin,
		})
		require.NoError(t, err)
		defer response.Body.Close()
		require.Equal(t, http.StatusOK, response.StatusCode)
		var process struct {
			PID string `json:"pid"`
		}
		require.NoError(t, json.NewDecoder(response.Body).Decode(&process))
		require.NotEmpty(t, process.PID)
		t.Cleanup(func() {
			if r, err := common.MakeRequest(http.MethodDelete, "/process/"+process.PID+"/kill", nil); err == nil {
				r.Body.Close()
			}
		})
		return process.PID
	}
	firstPID := start(`printf old; read gate; printf -- '-tail\n'`, true)
	first := openLogStream(t, firstPID, "application/x-ndjson")
	requireLogMediaType(t, first, "application/x-ndjson")
	reader := bufio.NewReader(first.Body)
	readNDJSONSource(t, reader, "stdout", "old")

	// The API allows reusing a name once the first instance has finished.
	// Its PID must still replay its own logs after the name is reused.
	require.Equal(t, http.StatusOK, writeStdin(t, firstPID, "continue").StatusCode)
	require.Equal(t, map[string]string{"stdout": "-tail\n", "stderr": ""}, collectNDJSONLogs(t, reader))
	secondPID := start(`printf 'new\n'; printf 'new-error\n' >&2`, false)
	require.NotEqual(t, firstPID, secondPID)
	second := openLogStream(t, secondPID, "application/x-ndjson")
	requireLogMediaType(t, second, "application/x-ndjson")
	require.Equal(t, map[string]string{"stdout": "new\n", "stderr": "new-error\n"}, collectNDJSONLogs(t, bufio.NewReader(second.Body)))

	for _, tc := range []struct{ pid, stdout, stderr string }{
		{firstPID, "old-tail\n", ""}, {secondPID, "new\n", "new-error\n"},
	} {
		replay := openLogStream(t, tc.pid, "application/x-ndjson")
		requireLogMediaType(t, replay, "application/x-ndjson")
		require.Equal(t, map[string]string{"stdout": tc.stdout, "stderr": tc.stderr}, collectNDJSONLogs(t, bufio.NewReader(replay.Body)))
	}
}
