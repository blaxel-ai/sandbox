package tests

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/blaxel-ai/sandbox-api/src/handler"
	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMultipartUploadBasic tests the basic multipart upload workflow
func TestMultipartUploadBasic(t *testing.T) {
	testPath := fmt.Sprintf("/tmp/multipart-test-%d.dat", time.Now().UnixNano())

	// Cleanup at the end
	defer func() {
		var successResp handler.SuccessResponse
		if resp, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testPath), nil, &successResp); err == nil {
			resp.Body.Close()
		}
	}()

	// Step 1: Initiate multipart upload
	initReq := map[string]interface{}{
		"permissions": "0644",
	}

	var initResp map[string]interface{}
	// Encode absolute paths by replacing leading / with %2F
	encodedPath := testPath
	if len(testPath) > 0 && testPath[0] == '/' {
		encodedPath = "%2F" + testPath[1:]
	}
	resp, err := common.MakeRequestAndParse(http.MethodPost, "/filesystem-multipart/initiate/"+encodedPath, initReq, &initResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, initResp["uploadId"])
	uploadID := initResp["uploadId"].(string)
	assert.Equal(t, testPath, initResp["path"])

	// Step 2: Upload three parts
	part1Content := []byte("This is part 1 content. ")
	part2Content := []byte("This is part 2 content. ")
	part3Content := []byte("This is part 3 content.")

	// Upload part 1
	var part1Resp map[string]interface{}
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=1", uploadID),
		part1Content,
		"part1.dat",
		nil,
	)
	require.NoError(t, err)
	err = common.ParseResponse(resp, &part1Resp)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, float64(1), part1Resp["partNumber"])
	assert.NotEmpty(t, part1Resp["etag"])
	part1ETag := part1Resp["etag"].(string)

	// Verify ETag is correct MD5
	expectedETag1 := md5Hash(part1Content)
	assert.Equal(t, expectedETag1, part1ETag)

	// Upload part 2
	var part2Resp map[string]interface{}
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=2", uploadID),
		part2Content,
		"part2.dat",
		nil,
	)
	require.NoError(t, err)
	err = common.ParseResponse(resp, &part2Resp)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	part2ETag := part2Resp["etag"].(string)

	// Upload part 3
	var part3Resp map[string]interface{}
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=3", uploadID),
		part3Content,
		"part3.dat",
		nil,
	)
	require.NoError(t, err)
	err = common.ParseResponse(resp, &part3Resp)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	part3ETag := part3Resp["etag"].(string)

	// Step 3: List parts
	var listPartsResp map[string]interface{}
	resp, err = common.MakeRequestAndParse(
		http.MethodGet,
		fmt.Sprintf("/filesystem-multipart/%s/parts", uploadID),
		nil,
		&listPartsResp,
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, uploadID, listPartsResp["uploadId"])
	parts := listPartsResp["parts"].([]interface{})
	assert.Len(t, parts, 3)

	// Step 4: Complete multipart upload
	completeReq := map[string]interface{}{
		"parts": []map[string]interface{}{
			{"partNumber": 1, "etag": part1ETag},
			{"partNumber": 2, "etag": part2ETag},
			{"partNumber": 3, "etag": part3ETag},
		},
	}

	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(
		http.MethodPost,
		fmt.Sprintf("/filesystem-multipart/%s/complete", uploadID),
		completeReq,
		&successResp,
	)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "success")

	// Step 5: Verify the final file exists and has correct content
	var fileResp filesystem.FileWithContent
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(testPath), nil, &fileResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	expectedContent := append(append(part1Content, part2Content...), part3Content...)
	assert.Equal(t, string(expectedContent), string(fileResp.Content))
}

// TestMultipartUploadOutOfOrder tests uploading parts out of order
func TestMultipartUploadOutOfOrder(t *testing.T) {
	testPath := fmt.Sprintf("/tmp/multipart-out-of-order-%d.dat", time.Now().UnixNano())

	defer func() {
		var successResp handler.SuccessResponse
		if resp, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testPath), nil, &successResp); err == nil {
			resp.Body.Close()
		}
	}()

	// Initiate upload
	var initResp map[string]interface{}
	// Encode absolute paths by replacing leading / with %2F
	encodedPath := testPath
	if len(testPath) > 0 && testPath[0] == '/' {
		encodedPath = "%2F" + testPath[1:]
	}
	resp, err := common.MakeRequestAndParse(http.MethodPost, "/filesystem-multipart/initiate/"+encodedPath, nil, &initResp)
	require.NoError(t, err)
	resp.Body.Close()
	uploadID := initResp["uploadId"].(string)

	// Upload parts in reverse order: 3, 2, 1
	part1Content := []byte("First")
	part2Content := []byte("Second")
	part3Content := []byte("Third")

	var etags [3]string

	// Upload part 3 first
	var partResp map[string]interface{}
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=3", uploadID),
		part3Content,
		"part3.dat",
		nil,
	)
	require.NoError(t, err)
	err = common.ParseResponse(resp, &partResp)
	require.NoError(t, err)
	resp.Body.Close()
	etags[2] = partResp["etag"].(string)

	// Upload part 2
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=2", uploadID),
		part2Content,
		"part2.dat",
		nil,
	)
	require.NoError(t, err)
	err = common.ParseResponse(resp, &partResp)
	require.NoError(t, err)
	resp.Body.Close()
	etags[1] = partResp["etag"].(string)

	// Upload part 1 last
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=1", uploadID),
		part1Content,
		"part1.dat",
		nil,
	)
	require.NoError(t, err)
	err = common.ParseResponse(resp, &partResp)
	require.NoError(t, err)
	resp.Body.Close()
	etags[0] = partResp["etag"].(string)

	// Complete upload
	completeReq := map[string]interface{}{
		"parts": []map[string]interface{}{
			{"partNumber": 1, "etag": etags[0]},
			{"partNumber": 2, "etag": etags[1]},
			{"partNumber": 3, "etag": etags[2]},
		},
	}

	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(
		http.MethodPost,
		fmt.Sprintf("/filesystem-multipart/%s/complete", uploadID),
		completeReq,
		&successResp,
	)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify final file content is in correct order
	var fileResp filesystem.FileWithContent
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(testPath), nil, &fileResp)
	require.NoError(t, err)
	resp.Body.Close()

	expectedContent := "FirstSecondThird"
	assert.Equal(t, expectedContent, string(fileResp.Content))
}

// TestMultipartUploadAbort tests aborting an upload
func TestMultipartUploadAbort(t *testing.T) {
	testPath := fmt.Sprintf("/tmp/multipart-abort-%d.dat", time.Now().UnixNano())

	// Initiate upload
	var initResp map[string]interface{}
	// Encode absolute paths by replacing leading / with %2F
	encodedPath := testPath
	if len(testPath) > 0 && testPath[0] == '/' {
		encodedPath = "%2F" + testPath[1:]
	}
	resp, err := common.MakeRequestAndParse(http.MethodPost, "/filesystem-multipart/initiate/"+encodedPath, nil, &initResp)
	require.NoError(t, err)
	resp.Body.Close()
	uploadID := initResp["uploadId"].(string)

	// Upload a part
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=1", uploadID),
		[]byte("Some content"),
		"part1.dat",
		nil,
	)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Abort the upload
	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(
		http.MethodDelete,
		fmt.Sprintf("/filesystem-multipart/%s/abort", uploadID),
		nil,
		&successResp,
	)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Contains(t, successResp.Message, "aborted")

	// Try to upload another part (should fail)
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=2", uploadID),
		[]byte("More content"),
		"part2.dat",
		nil,
	)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// Verify the file was not created
	resp, err = common.MakeRequest(http.MethodGet, common.EncodeFilesystemPath(testPath), nil)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// TestMultipartUploadListUploads tests listing all active uploads
func TestMultipartUploadListUploads(t *testing.T) {
	testPath1 := fmt.Sprintf("/tmp/multipart-list-1-%d.dat", time.Now().UnixNano())
	testPath2 := fmt.Sprintf("/tmp/multipart-list-2-%d.dat", time.Now().UnixNano())

	// Initiate two uploads
	var initResp1, initResp2 map[string]interface{}

	encodedPath1 := testPath1
	if len(testPath1) > 0 && testPath1[0] == '/' {
		encodedPath1 = "%2F" + testPath1[1:]
	}
	resp1, err := common.MakeRequestAndParse(http.MethodPost, "/filesystem-multipart/initiate/"+encodedPath1, nil, &initResp1)
	require.NoError(t, err)
	resp1.Body.Close()
	uploadID1 := initResp1["uploadId"].(string)

	encodedPath2 := testPath2
	if len(testPath2) > 0 && testPath2[0] == '/' {
		encodedPath2 = "%2F" + testPath2[1:]
	}
	resp2, err := common.MakeRequestAndParse(http.MethodPost, "/filesystem-multipart/initiate/"+encodedPath2, nil, &initResp2)
	require.NoError(t, err)
	resp2.Body.Close()
	uploadID2 := initResp2["uploadId"].(string)

	// Cleanup
	defer func() {
		common.MakeRequest(http.MethodDelete, fmt.Sprintf("/filesystem-multipart/%s/abort", uploadID1), nil)
		common.MakeRequest(http.MethodDelete, fmt.Sprintf("/filesystem-multipart/%s/abort", uploadID2), nil)
	}()

	// List all uploads
	var listResp map[string]interface{}
	resp, err := common.MakeRequestAndParse(http.MethodGet, "/filesystem-multipart", nil, &listResp)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	uploads := listResp["uploads"].([]interface{})
	assert.GreaterOrEqual(t, len(uploads), 2, "Should have at least 2 active uploads")

	// Verify our uploads are in the list
	foundUpload1 := false
	foundUpload2 := false
	for _, upload := range uploads {
		uploadMap := upload.(map[string]interface{})
		if uploadMap["uploadId"] == uploadID1 {
			foundUpload1 = true
		}
		if uploadMap["uploadId"] == uploadID2 {
			foundUpload2 = true
		}
	}
	assert.True(t, foundUpload1, "Upload 1 should be in the list")
	assert.True(t, foundUpload2, "Upload 2 should be in the list")
}

// TestMultipartUploadInvalidPartNumber tests error handling for invalid part numbers
func TestMultipartUploadInvalidPartNumber(t *testing.T) {
	testPath := fmt.Sprintf("/tmp/multipart-invalid-part-%d.dat", time.Now().UnixNano())

	var initResp map[string]interface{}
	// Encode absolute paths by replacing leading / with %2F
	encodedPath := testPath
	if len(testPath) > 0 && testPath[0] == '/' {
		encodedPath = "%2F" + testPath[1:]
	}
	resp, err := common.MakeRequestAndParse(http.MethodPost, "/filesystem-multipart/initiate/"+encodedPath, nil, &initResp)
	require.NoError(t, err)
	resp.Body.Close()
	uploadID := initResp["uploadId"].(string)

	defer func() {
		common.MakeRequest(http.MethodDelete, fmt.Sprintf("/filesystem/multipart/%s/abort", uploadID), nil)
	}()

	// Try part number 0 (should fail)
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=0", uploadID),
		[]byte("content"),
		"part.dat",
		nil,
	)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	// Try part number 10001 (should fail)
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=10001", uploadID),
		[]byte("content"),
		"part.dat",
		nil,
	)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// TestMultipartUploadWrongETag tests error handling for incorrect ETags
func TestMultipartUploadWrongETag(t *testing.T) {
	testPath := fmt.Sprintf("/tmp/multipart-wrong-etag-%d.dat", time.Now().UnixNano())

	var initResp map[string]interface{}
	// Encode absolute paths by replacing leading / with %2F
	encodedPath := testPath
	if len(testPath) > 0 && testPath[0] == '/' {
		encodedPath = "%2F" + testPath[1:]
	}
	resp, err := common.MakeRequestAndParse(http.MethodPost, "/filesystem-multipart/initiate/"+encodedPath, nil, &initResp)
	require.NoError(t, err)
	resp.Body.Close()
	uploadID := initResp["uploadId"].(string)

	defer func() {
		common.MakeRequest(http.MethodDelete, fmt.Sprintf("/filesystem/multipart/%s/abort", uploadID), nil)
	}()

	// Upload a part
	var partResp map[string]interface{}
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=1", uploadID),
		[]byte("test content"),
		"part1.dat",
		nil,
	)
	require.NoError(t, err)
	err = common.ParseResponse(resp, &partResp)
	require.NoError(t, err)
	resp.Body.Close()

	// Try to complete with wrong ETag
	completeReq := map[string]interface{}{
		"parts": []map[string]interface{}{
			{"partNumber": 1, "etag": "wrongetag123456789"},
		},
	}

	resp, err = common.MakeRequest(
		http.MethodPost,
		fmt.Sprintf("/filesystem-multipart/%s/complete", uploadID),
		completeReq,
	)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// TestMultipartUploadMissingPart tests error handling when completing without all parts
func TestMultipartUploadMissingPart(t *testing.T) {
	testPath := fmt.Sprintf("/tmp/multipart-missing-part-%d.dat", time.Now().UnixNano())

	var initResp map[string]interface{}
	// Encode absolute paths by replacing leading / with %2F
	encodedPath := testPath
	if len(testPath) > 0 && testPath[0] == '/' {
		encodedPath = "%2F" + testPath[1:]
	}
	resp, err := common.MakeRequestAndParse(http.MethodPost, "/filesystem-multipart/initiate/"+encodedPath, nil, &initResp)
	require.NoError(t, err)
	resp.Body.Close()
	uploadID := initResp["uploadId"].(string)

	defer func() {
		common.MakeRequest(http.MethodDelete, fmt.Sprintf("/filesystem/multipart/%s/abort", uploadID), nil)
	}()

	// Upload only part 1
	var partResp map[string]interface{}
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=1", uploadID),
		[]byte("part 1"),
		"part1.dat",
		nil,
	)
	require.NoError(t, err)
	err = common.ParseResponse(resp, &partResp)
	require.NoError(t, err)
	resp.Body.Close()
	part1ETag := partResp["etag"].(string)

	// Try to complete with parts 1 and 2, but part 2 was never uploaded
	completeReq := map[string]interface{}{
		"parts": []map[string]interface{}{
			{"partNumber": 1, "etag": part1ETag},
			{"partNumber": 2, "etag": "fakeetag"},
		},
	}

	resp, err = common.MakeRequest(
		http.MethodPost,
		fmt.Sprintf("/filesystem-multipart/%s/complete", uploadID),
		completeReq,
	)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

// TestMultipartUploadLargeFile tests uploading a larger file in multiple parts
func TestMultipartUploadLargeFile(t *testing.T) {
	testPath := fmt.Sprintf("/tmp/multipart-large-%d.dat", time.Now().UnixNano())

	defer func() {
		var successResp handler.SuccessResponse
		if resp, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testPath), nil, &successResp); err == nil {
			resp.Body.Close()
		}
	}()

	// Initiate upload
	var initResp map[string]interface{}
	// Encode absolute paths by replacing leading / with %2F
	encodedPath := testPath
	if len(testPath) > 0 && testPath[0] == '/' {
		encodedPath = "%2F" + testPath[1:]
	}
	resp, err := common.MakeRequestAndParse(http.MethodPost, "/filesystem-multipart/initiate/"+encodedPath, nil, &initResp)
	require.NoError(t, err)
	resp.Body.Close()
	uploadID := initResp["uploadId"].(string)

	// Create 10 parts of 100KB each
	partSize := 100 * 1024 // 100KB
	numParts := 10
	var etags []string
	var allContent bytes.Buffer

	for i := 1; i <= numParts; i++ {
		// Generate part content
		partContent := make([]byte, partSize)
		for j := 0; j < partSize; j++ {
			partContent[j] = byte(i + j%256)
		}
		allContent.Write(partContent)

		// Upload part
		var partResp map[string]interface{}
		resp, err = common.MakeMultipartRequest(
			http.MethodPut,
			fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=%d", uploadID, i),
			partContent,
			fmt.Sprintf("part%d.dat", i),
			nil,
		)
		require.NoError(t, err)
		err = common.ParseResponse(resp, &partResp)
		require.NoError(t, err)
		resp.Body.Close()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		etags = append(etags, partResp["etag"].(string))
	}

	// Complete upload
	parts := make([]map[string]interface{}, numParts)
	for i := 0; i < numParts; i++ {
		parts[i] = map[string]interface{}{
			"partNumber": i + 1,
			"etag":       etags[i],
		}
	}

	completeReq := map[string]interface{}{
		"parts": parts,
	}

	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(
		http.MethodPost,
		fmt.Sprintf("/filesystem-multipart/%s/complete", uploadID),
		completeReq,
		&successResp,
	)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify file size
	info, err := os.Stat(testPath)
	require.NoError(t, err)
	expectedSize := int64(partSize * numParts)
	assert.Equal(t, expectedSize, info.Size())

	// Read file and verify content matches
	fileContent, err := os.ReadFile(testPath)
	require.NoError(t, err)
	assert.Equal(t, allContent.Bytes(), fileContent)
}

// TestMultipartUploadReuploaDPart tests re-uploading the same part number
func TestMultipartUploadReuploadPart(t *testing.T) {
	testPath := fmt.Sprintf("/tmp/multipart-reupload-%d.dat", time.Now().UnixNano())

	defer func() {
		var successResp handler.SuccessResponse
		if resp, err := common.MakeRequestAndParse(http.MethodDelete, common.EncodeFilesystemPath(testPath), nil, &successResp); err == nil {
			resp.Body.Close()
		}
	}()

	var initResp map[string]interface{}
	// Encode absolute paths by replacing leading / with %2F
	encodedPath := testPath
	if len(testPath) > 0 && testPath[0] == '/' {
		encodedPath = "%2F" + testPath[1:]
	}
	resp, err := common.MakeRequestAndParse(http.MethodPost, "/filesystem-multipart/initiate/"+encodedPath, nil, &initResp)
	require.NoError(t, err)
	resp.Body.Close()
	uploadID := initResp["uploadId"].(string)

	// Upload part 1 with initial content
	var partResp map[string]interface{}
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=1", uploadID),
		[]byte("initial content"),
		"part1.dat",
		nil,
	)
	require.NoError(t, err)
	err = common.ParseResponse(resp, &partResp)
	require.NoError(t, err)
	resp.Body.Close()
	initialETag := partResp["etag"].(string)

	// Re-upload part 1 with different content
	newContent := []byte("updated content")
	resp, err = common.MakeMultipartRequest(
		http.MethodPut,
		fmt.Sprintf("/filesystem-multipart/%s/part?partNumber=1", uploadID),
		newContent,
		"part1.dat",
		nil,
	)
	require.NoError(t, err)
	err = common.ParseResponse(resp, &partResp)
	require.NoError(t, err)
	resp.Body.Close()
	newETag := partResp["etag"].(string)

	// ETags should be different
	assert.NotEqual(t, initialETag, newETag)

	// Complete with new ETag
	completeReq := map[string]interface{}{
		"parts": []map[string]interface{}{
			{"partNumber": 1, "etag": newETag},
		},
	}

	var successResp handler.SuccessResponse
	resp, err = common.MakeRequestAndParse(
		http.MethodPost,
		fmt.Sprintf("/filesystem-multipart/%s/complete", uploadID),
		completeReq,
		&successResp,
	)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify final file has the updated content
	var fileResp filesystem.FileWithContent
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(testPath), nil, &fileResp)
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, string(newContent), string(fileResp.Content))
}

// md5Hash calculates the MD5 hash of data and returns it as hex string
func md5Hash(data []byte) string {
	hash := md5.New()
	hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil))
}
