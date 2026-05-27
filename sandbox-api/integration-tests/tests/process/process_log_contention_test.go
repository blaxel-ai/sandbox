package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessDetailsStayResponsiveWithBackpressuredLogStream(t *testing.T) {
	processName := fmt.Sprintf("test-loglock-contention-%d", time.Now().UnixNano())
	streamClient, closeStreamClient := newBackpressuredHTTPClient()
	defer closeStreamClient()

	processRequest := map[string]interface{}{
		"name":       processName,
		"command":    "sleep 1; dd if=/dev/zero bs=1048576 count=8 2>/dev/null | tr '\\000' x",
		"workingDir": "/",
	}

	streamResp, err := makeRequestWithClient(streamClient, http.MethodPost, "/process", processRequest, map[string]string{
		"Accept": "text/event-stream",
	})
	require.NoError(t, err)
	defer streamResp.Body.Close()
	require.Equal(t, http.StatusOK, streamResp.StatusCode)

	waitForProcessVisible(t, processName, 3*time.Second)

	// Keep the stream open without reading it so the server-side writer gets
	// backpressured while the process drains a large log burst.
	time.Sleep(3 * time.Second)

	start := time.Now()
	detailResp, err := common.MakeRequestWithTimeout(http.MethodGet, "/process/"+processName, nil, 5*time.Second)
	elapsed := time.Since(start)
	require.NoError(t, err, "process details should not block behind log stream writes")
	defer detailResp.Body.Close()
	require.Equal(t, http.StatusOK, detailResp.StatusCode)
	assert.Less(t, elapsed, 4*time.Second, "GET /process/{name} should stay responsive while log streaming is backpressured")
}

func newBackpressuredHTTPClient() (*http.Client, func()) {
	transport := &http.Transport{
		DisableCompression: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			var dialer net.Dialer
			conn, err := dialer.DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			if tcpConn, ok := conn.(*net.TCPConn); ok {
				_ = tcpConn.SetReadBuffer(1024)
			}
			return conn, nil
		},
	}

	client := &http.Client{Transport: transport}
	return client, transport.CloseIdleConnections
}

func makeRequestWithClient(client *http.Client, method, path string, body interface{}, headers map[string]string) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("error marshaling JSON: %w", err)
		}
		bodyReader = bytes.NewBuffer(jsonData)
	}

	req, err := http.NewRequest(method, common.BaseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("error creating request: %w", err)
	}

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}

	return client.Do(req)
}

func waitForProcessVisible(t *testing.T, processName string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	var lastStatus int
	for time.Now().Before(deadline) {
		resp, err := common.MakeRequestWithTimeout(http.MethodGet, "/process/"+processName, nil, 500*time.Millisecond)
		if err == nil {
			lastStatus = resp.StatusCode
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	require.Failf(t, "process did not become visible", "process %q was not visible before timeout, last status: %d", processName, lastStatus)
}
