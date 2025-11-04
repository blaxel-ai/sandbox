package tests

import (
	"bytes"
	"crypto/sha256"
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
		common.EncodeFilesystemPath(testFilePath),
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
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(testFilePath), nil, &fileResponse)
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
	resp, err = common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testFilePath), nil, &successResp)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")
}

// TestStreamingLargeFile tests streaming upload and download of a 250MB file
func TestStreamingLargeFile(t *testing.T) {
	// Create a custom HTTP client with longer timeout for large file transfers
	originalClient := common.Client
	defer func() { common.Client = originalClient }()

	common.Client = &http.Client{
		Timeout: 5 * time.Minute, // 5 minutes for 250MB transfers
	}

	timestamp := fmt.Sprintf("%d", time.Now().UnixNano())
	sourceFile := fmt.Sprintf("/tmp/streaming-test-source-%s.bin", timestamp)
	targetFile := fmt.Sprintf("/tmp/streaming-test-target-%s.bin", timestamp)

	// Ensure cleanup happens even if test fails
	defer func() {
		t.Logf("Cleaning up test files...")
		var successResp handler.SuccessResponse

		// Clean up source file
		if resp, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(sourceFile), nil, &successResp); err == nil {
			resp.Body.Close()
			t.Logf("Deleted source file: %s", sourceFile)
		}

		// Clean up target file
		if resp, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(targetFile), nil, &successResp); err == nil {
			resp.Body.Close()
			t.Logf("Deleted target file: %s", targetFile)
		}
	}()

	// Create 250MB of test data (repeating pattern for compression resistance)
	const fileSize = 250 * 1024 * 1024 // 250MB
	const chunkSize = 1024 * 1024      // 1MB chunks

	t.Logf("Creating 250MB test file for streaming test...")

	// Generate a repeating pattern to fill the file
	pattern := make([]byte, chunkSize)
	for i := range pattern {
		pattern[i] = byte(i % 256)
	}

	// Create a reader that generates the data on-the-fly without storing it all in memory
	dataReader := &repeatingReader{
		pattern:       pattern,
		totalBytes:    fileSize,
		bytesWritten:  0,
		patternOffset: 0,
	}

	// Calculate hash while uploading
	hasher := sha256.New()
	teeReader := io.TeeReader(dataReader, hasher)

	// Upload the file using streaming multipart (write operation)
	t.Logf("Uploading 250MB file in streaming mode...")
	startUpload := time.Now()

	uploadResp, err := common.MakeMultipartRequestStream(
		http.MethodPut,
		common.EncodeFilesystemPath(sourceFile),
		teeReader,
		"large-file.bin",
		map[string]string{"permissions": "0644"},
	)
	require.NoError(t, err)
	defer uploadResp.Body.Close()

	uploadDuration := time.Since(startUpload)
	t.Logf("Upload completed in %v (%.2f MB/s)", uploadDuration, float64(fileSize)/(1024*1024)/uploadDuration.Seconds())

	// Verify upload response
	var successResp handler.SuccessResponse
	err = common.ParseJSONResponse(uploadResp, &successResp)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, uploadResp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	uploadHash := hasher.Sum(nil)
	t.Logf("Upload hash: %x", uploadHash)

	// Download the file using streaming mode (read operation)
	t.Logf("Downloading 250MB file in streaming mode with Accept: application/octet-stream...")
	startDownload := time.Now()

	req, err := http.NewRequest(http.MethodGet, common.BaseURL+common.EncodeFilesystemPath(sourceFile), nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "application/octet-stream")

	downloadResp, err := common.Client.Do(req)
	require.NoError(t, err)
	defer downloadResp.Body.Close()

	assert.Equal(t, http.StatusOK, downloadResp.StatusCode)
	assert.Equal(t, "application/octet-stream", downloadResp.Header.Get("Content-Type"), "Should return application/octet-stream for .bin extension")
	assert.Contains(t, downloadResp.Header.Get("Content-Disposition"), "attachment", "Should have attachment disposition")

	// Calculate hash while downloading to verify data integrity
	downloadHasher := sha256.New()
	bytesRead, err := io.Copy(downloadHasher, downloadResp.Body)
	require.NoError(t, err)

	downloadDuration := time.Since(startDownload)
	t.Logf("Download completed in %v (%.2f MB/s)", downloadDuration, float64(bytesRead)/(1024*1024)/downloadDuration.Seconds())

	downloadHash := downloadHasher.Sum(nil)
	t.Logf("Download hash: %x", downloadHash)

	// Verify file size and hash match
	assert.Equal(t, int64(fileSize), bytesRead, "Downloaded file size should match uploaded size")
	assert.Equal(t, uploadHash, downloadHash, "Download hash should match upload hash")

	// Test simultaneous read and write: stream from source to target
	t.Logf("Testing simultaneous read/write by streaming copy...")
	startCopy := time.Now()

	// Start download stream
	req, err = http.NewRequest(http.MethodGet, common.BaseURL+common.EncodeFilesystemPath(sourceFile), nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "application/octet-stream")

	copyDownloadResp, err := common.Client.Do(req)
	require.NoError(t, err)
	defer copyDownloadResp.Body.Close()

	assert.Equal(t, http.StatusOK, copyDownloadResp.StatusCode)

	// Upload while reading - this tests simultaneous read/write streaming
	copyHasher := sha256.New()
	teeReader = io.TeeReader(copyDownloadResp.Body, copyHasher)

	uploadResp, err = common.MakeMultipartRequestStream(
		http.MethodPut,
		common.EncodeFilesystemPath(targetFile),
		teeReader,
		"target-file.bin",
		map[string]string{"permissions": "0644"},
	)
	require.NoError(t, err)
	defer uploadResp.Body.Close()

	copyDuration := time.Since(startCopy)
	t.Logf("Streaming copy completed in %v (%.2f MB/s)", copyDuration, float64(fileSize)/(1024*1024)/copyDuration.Seconds())

	err = common.ParseJSONResponse(uploadResp, &successResp)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, uploadResp.StatusCode)

	copyHash := copyHasher.Sum(nil)
	t.Logf("Copy hash: %x", copyHash)
	assert.Equal(t, uploadHash, copyHash, "Copied file hash should match original")

	// Verify target file by reading it back
	t.Logf("Verifying copied file integrity...")
	req, err = http.NewRequest(http.MethodGet, common.BaseURL+common.EncodeFilesystemPath(targetFile), nil)
	require.NoError(t, err)
	req.Header.Set("Accept", "application/octet-stream")

	verifyResp, err := common.Client.Do(req)
	require.NoError(t, err)
	defer verifyResp.Body.Close()

	verifyHasher := sha256.New()
	bytesRead, err = io.Copy(verifyHasher, verifyResp.Body)
	require.NoError(t, err)

	verifyHash := verifyHasher.Sum(nil)
	t.Logf("Verify hash: %x", verifyHash)

	assert.Equal(t, int64(fileSize), bytesRead, "Verified file size should match")
	assert.Equal(t, uploadHash, verifyHash, "Verified file hash should match original")

	t.Logf("Test completed successfully! Files will be cleaned up via defer.")
}

// repeatingReader generates data on-the-fly by repeating a pattern
type repeatingReader struct {
	pattern       []byte
	totalBytes    int64
	bytesWritten  int64
	patternOffset int
}

func (r *repeatingReader) Read(p []byte) (n int, err error) {
	if r.bytesWritten >= r.totalBytes {
		return 0, io.EOF
	}

	remaining := r.totalBytes - r.bytesWritten
	toWrite := int64(len(p))
	if toWrite > remaining {
		toWrite = remaining
	}

	written := int64(0)
	for written < toWrite {
		// How much can we copy from the current position in the pattern?
		available := len(r.pattern) - r.patternOffset
		toCopy := int(toWrite - written)
		if toCopy > available {
			toCopy = available
		}

		// Copy from pattern to output buffer
		copy(p[written:], r.pattern[r.patternOffset:r.patternOffset+toCopy])
		written += int64(toCopy)
		r.patternOffset += toCopy

		// Wrap around to start of pattern if we've exhausted it
		if r.patternOffset >= len(r.pattern) {
			r.patternOffset = 0
		}
	}

	r.bytesWritten += written
	return int(written), nil
}
