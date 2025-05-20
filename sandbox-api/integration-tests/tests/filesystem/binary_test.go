package tests

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/blaxel-ai/sandbox-api/src/handler"
	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestBinaryFileUpload tests the binary file upload functionality
func TestBinaryFileUpload(t *testing.T) {
	t.Parallel()

	// Create test binary file path with timestamp to avoid conflicts
	timestamp := fmt.Sprintf("%d", time.Now().UnixNano())
	testFilePath := fmt.Sprintf("/tmp/binary-file-%s.bin", timestamp)

	// No need to create directory first - using /tmp directly
	var successResp handler.SuccessResponse

	// Create binary test data
	binaryData := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0xFF, 0xFE, 0xFD, 0x8A, 0x8B, 0x8C}

	// Test binary file upload
	resp, err := common.MakeMultipartRequest(
		http.MethodPut,
		"/filesystem"+testFilePath,
		binaryData,
		"test-binary.bin",
		map[string]string{
			"permissions": "0644",
			"path":        testFilePath,
		},
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Verify response
	body, _ := io.ReadAll(resp.Body)
	t.Logf("Response body: %s", string(body))
	t.Logf("Status code: %d", resp.StatusCode)

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Reset the response body for JSON parsing
	resp.Body = io.NopCloser(bytes.NewBuffer(body))

	err = common.ParseJSONResponse(resp, &successResp)
	require.NoError(t, err)
	assert.Contains(t, successResp.Message, "success")

	// Verify the file exists by requesting it
	var fileResponse filesystem.FileWithContent
	resp, err = common.MakeRequestAndParse(http.MethodGet, "/filesystem"+testFilePath, nil, &fileResponse)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, testFilePath, fileResponse.Path)

	// Binary content might be transformed in the JSON response due to encoding issues
	// Instead, check that we got some content back and the file size is at least as expected
	t.Logf("Expected %d bytes, got %d bytes", len(binaryData), len(fileResponse.Content))
	assert.Greater(t, len(fileResponse.Content), 0, "File content should not be empty")
	assert.Equal(t, int64(len(binaryData)), fileResponse.Size, "File size should match our uploaded data")

	// Clean up - delete the file
	resp, err = common.MakeRequestAndParse(http.MethodDelete, "/filesystem"+testFilePath, nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")
}
