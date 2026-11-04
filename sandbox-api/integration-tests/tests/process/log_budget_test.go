package tests

import (
	"bufio"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/require"
)

// The log budget used to be split evenly across every process the sandbox had
// ever run, finished ones included. A sandbox that ran a few hundred short
// commands squeezed a long-lived process down to its last ~190 KB of output,
// although the finished processes' logs held almost nothing (ENG-6187).
//
// The test reads the guest's memory to work out the share the old even split
// would have given, then writes several times that: the old code truncates it,
// a budget shared by bytes actually used keeps all of it.
func TestBusyProcessKeepsItsOutputNextToManyFinishedOnes(t *testing.T) {
	const (
		finished        = 300
		lineBytes       = 400              // one event line, newline included
		journalOverhead = 2                // the combined journal is base64 JSONL, ~1.5x raw
		perFileCeiling  = 32 * 1024 * 1024 // maxLogFileBytes
		shareFloor      = 256 * 1024       // minLogFileBytes
	)

	memTotal := guestMemTotal(t)

	var wg sync.WaitGroup
	sem := make(chan struct{}, 20)
	for i := 0; i < finished; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(i int) {
			defer wg.Done()
			defer func() { <-sem }()
			runToCompletion(t, fmt.Sprintf("echo short-%d", i))
		}(i)
	}
	wg.Wait()

	var processes []map[string]interface{}
	resp, err := common.MakeRequestAndParse(http.MethodGet, "/process", nil, &processes)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// What one log file got under the even split, per process ever run.
	evenShare := memTotal / 10 / int64(3*(len(processes)+1))
	if evenShare < shareFloor {
		evenShare = shareFloor
	}
	written := 3 * evenShare
	if written*journalOverhead > perFileCeiling {
		t.Skipf("guest has %d bytes of memory: the even split (%d per file) is too large to exceed under the per-file ceiling", memTotal, evenShare)
	}
	events := int(written / lineBytes)

	name := uniqueProcessName("busy-log-budget")
	command := fmt.Sprintf(
		`awk 'BEGIN { for (i = 1; i <= %d; i++) printf "{\"event\":%%d,\"pad\":\"%%0380d\"}\n", i, 0 }'; sleep 60`,
		events,
	)
	resp, err = common.MakeRequest(http.MethodPost, "/process", map[string]interface{}{"command": command, "name": name})
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	defer func() {
		if resp, err := common.MakeRequest(http.MethodDelete, "/process/"+name+"/kill", nil); err == nil {
			resp.Body.Close()
		}
	}()

	last := fmt.Sprintf(`{"event":%d,`, events)
	require.Eventually(t, func() bool {
		var logs map[string]interface{}
		resp, err := common.MakeRequestAndParse(http.MethodGet, "/process/"+name+"/logs", nil, &logs)
		if err != nil || resp.StatusCode != http.StatusOK {
			return false
		}
		stdout, _ := logs["stdout"].(string)
		return strings.Contains(stdout, last)
	}, 30*time.Second, 500*time.Millisecond, "process never wrote its last event")

	// Let at least one cap sweep (every 5 s) run over the finished output.
	time.Sleep(7 * time.Second)

	first, count, truncated := readStreamedEvents(t, name, events)
	require.False(t, truncated, "output was truncated")
	require.Equal(t, 1, first, "the start of the output was released")
	require.Equal(t, events, count, "events lost from the replay")
}

// guestMemTotal reads MemTotal from inside the sandbox, in bytes.
func guestMemTotal(t *testing.T) int64 {
	t.Helper()
	out := runToCompletion(t, `awk '/^MemTotal:/ { print $2 }' /proc/meminfo`)
	kb, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	require.NoError(t, err, "MemTotal = %q", out)
	return kb * 1024
}

func runToCompletion(t *testing.T, command string) string {
	t.Helper()
	var result map[string]interface{}
	resp, err := common.MakeRequestAndParse(http.MethodPost, "/process", map[string]interface{}{
		"command":           command,
		"waitForCompletion": true,
	}, &result)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	stdout, _ := result["stdout"].(string)
	return stdout
}

// readStreamedEvents replays a process' output through logs/stream until want
// events arrived or the stream went quiet, and reports the first event number,
// how many arrived, and whether a truncation marker was sent.
func readStreamedEvents(t *testing.T, name string, want int) (first, count int, truncated bool) {
	t.Helper()
	resp, err := common.MakeRequestWithTimeout(http.MethodGet, "/process/"+name+"/logs/stream", nil, 30*time.Second)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for count < want && scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "truncated") {
			truncated = true
		}
		i := strings.Index(line, `{"event":`)
		if i < 0 {
			continue
		}
		rest := line[i+len(`{"event":`):]
		n, err := strconv.Atoi(rest[:strings.IndexByte(rest, ',')])
		require.NoError(t, err, "line %q", line)
		if count == 0 {
			first = n
		}
		count++
	}
	return first, count, truncated
}
