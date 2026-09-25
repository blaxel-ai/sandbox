package tests

import (
	"bufio"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A log stream must survive a restart of the process it follows.
//
// The handler used to wait on the per-run Done channel, which a restart closes
// before installing a fresh one. The stream then ended with 200 and no error
// the moment the process bounced, while the process came back up and kept
// producing output nobody received: from the caller's side the process looks
// alive, healthy and silent, with nothing in the logs explaining the silence.
func TestLogStreamSurvivesRestart(t *testing.T) {
	name := fmt.Sprintf("restarter-%d", time.Now().UnixNano())
	marker := "/tmp/" + name + ".once"

	// Prints run1, fails, restarts, then prints run2 and stays up. "run2" only
	// exists after the restart, so receiving it proves the stream carried across.
	command := fmt.Sprintf(
		`sh -c 'if [ ! -f %s ]; then touch %s; echo run1; exit 1; fi; echo run2; sleep 20'`,
		marker, marker,
	)
	resp, err := common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
		"command":          command,
		"name":             name,
		"restartOnFailure": true,
		"maxRestarts":      3,
	})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	streamResp, err := common.MakeRequestWithTimeout(
		http.MethodGet, "/process/"+name+"/logs/stream", nil, 30*time.Second)
	require.NoError(t, err)
	defer streamResp.Body.Close()
	require.Equal(t, http.StatusOK, streamResp.StatusCode)

	linesCh := make(chan string, 32)
	go func() {
		reader := bufio.NewReader(streamResp.Body)
		for {
			line, err := reader.ReadString('\n')
			if line != "" {
				linesCh <- line
			}
			if err != nil {
				close(linesCh)
				return
			}
		}
	}()

	var seen []string
	deadline := time.After(25 * time.Second)
	for {
		select {
		case line, ok := <-linesCh:
			if !ok {
				t.Fatalf("stream closed before the restarted process produced output; saw: %v", seen)
			}
			if strings.HasPrefix(line, "[keepalive]") {
				continue
			}
			seen = append(seen, strings.TrimRight(line, "\n"))
			if strings.Contains(line, "run2") {
				// The restart notice belongs in the stream: a reader should be
				// able to tell the output came from a new run.
				assert.Contains(t, strings.Join(seen, "\n"), "Attempting restart",
					"the restart should be announced in the stream")

				// And the process is the same one, still running.
				var info map[string]interface{}
				_, err := common.MakeRequestAndParse(http.MethodGet, "/process/"+name, nil, &info)
				require.NoError(t, err)
				assert.Equal(t, "running", info["status"])
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for post-restart output; saw: %v", seen)
		}
	}
}

// A stream whose process is quiet must stay open: the keepalive is what holds
// it, and it used to stop for good at the first restart, because the status is
// briefly not "running" while the process bounces.
func TestQuietStreamStaysOpenAfterRestart(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for keepalives")
	}
	name := fmt.Sprintf("quiet-restarter-%d", time.Now().UnixNano())
	marker := "/tmp/" + name + ".once"

	// Fails once, then says nothing at all for the rest of the test.
	command := fmt.Sprintf(
		`sh -c 'if [ ! -f %s ]; then touch %s; exit 1; fi; sleep 120'`,
		marker, marker,
	)
	resp, err := common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{
		"command":          command,
		"name":             name,
		"restartOnFailure": true,
		"maxRestarts":      3,
	})
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	streamResp, err := common.MakeRequestWithTimeout(
		http.MethodGet, "/process/"+name+"/logs/stream", nil, 60*time.Second)
	require.NoError(t, err)
	defer streamResp.Body.Close()

	keepalives := make(chan string, 8)
	go func() {
		reader := bufio.NewReader(streamResp.Body)
		for {
			line, err := reader.ReadString('\n')
			if strings.HasPrefix(line, "[keepalive]") {
				keepalives <- line
			}
			if err != nil {
				close(keepalives)
				return
			}
		}
	}()

	// One keepalive after the restart is enough: it can only arrive if the
	// goroutine outlived the bounce.
	select {
	case _, ok := <-keepalives:
		require.True(t, ok, "stream closed before any keepalive arrived after the restart")
	case <-time.After(50 * time.Second):
		t.Fatal("no keepalive within 50s: the stream went silent after the restart")
	}
}
