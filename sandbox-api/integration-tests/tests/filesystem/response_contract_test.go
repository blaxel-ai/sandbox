package tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/blaxel-ai/sandbox-api/src/handler/filesystem"
	"github.com/stretchr/testify/require"
)

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	resp, err := common.MakeRequest(http.MethodPut, common.EncodeFilesystemPath(path), map[string]interface{}{"content": content})
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func uniqueTestDir(prefix string) string {
	return fmt.Sprintf("/tmp/%s-%d", prefix, time.Now().UnixNano())
}

func TestFileWithContentHasBaseName(t *testing.T) {
	dir := uniqueTestDir("fs-name")
	path := dir + "/hello.txt"
	writeTestFile(t, path, "hello")
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})

	var file filesystem.FileWithContent
	resp, err := common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(path), nil, &file)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "hello.txt", file.Name)
}

func encodeRoute(prefix, path string) string {
	return prefix + "%2F" + strings.TrimPrefix(path, "/")
}

func TestSearchesReturnEmptyMatchesArray(t *testing.T) {
	dir := uniqueTestDir("fs-empty-search")
	writeTestFile(t, dir+"/file.txt", "hello")
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})

	for _, target := range []string{
		encodeRoute("/filesystem-content-search", dir) + "?query=nothing-matches-this",
		encodeRoute("/filesystem-find", dir) + "?patterns=*.nothing",
		encodeRoute("/filesystem-search", dir) + "?query=nothing-matches-this",
	} {
		resp, err := common.MakeRequest(http.MethodGet, target, nil)
		require.NoError(t, err)
		var body map[string]json.RawMessage
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode, target)
		require.Equal(t, "[]", string(body["matches"]), target)
	}
}

func TestMultipartUploadAppliesPermissionsSentAfterFile(t *testing.T) {
	dir := uniqueTestDir("fs-multipart-perms")
	path := dir + "/script.sh"
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})

	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "script.sh")
	require.NoError(t, err)
	_, err = part.Write([]byte("#!/bin/sh\necho hi\n"))
	require.NoError(t, err)
	require.NoError(t, form.WriteField("permissions", "0755"))
	require.NoError(t, form.WriteField("path", path))
	require.NoError(t, form.Close())

	request, err := http.NewRequest(http.MethodPut, common.BaseURL+common.EncodeFilesystemPath(path), &body)
	require.NoError(t, err)
	request.Header.Set("Content-Type", form.FormDataContentType())
	resp, err := common.Client.Do(request)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var file filesystem.FileWithContent
	resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(path), nil, &file)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, "755", file.Permissions)
}

func TestMultipartUploadPermissionsOrderAndExistingMode(t *testing.T) {
	dir := uniqueTestDir("fs-mode-order")
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})
	for _, first := range []bool{true, false} {
		path := fmt.Sprintf("%s/%v", dir, first)
		for _, requested := range []string{"0777", "0600"} {
			var body bytes.Buffer
			form := multipart.NewWriter(&body)
			if first {
				require.NoError(t, form.WriteField("permissions", requested))
			}
			part, err := form.CreateFormFile("file", "file")
			require.NoError(t, err)
			_, err = part.Write([]byte("data"))
			require.NoError(t, err)
			if !first {
				require.NoError(t, form.WriteField("permissions", requested))
			}
			require.NoError(t, form.Close())
			request, err := http.NewRequest(http.MethodPut, common.BaseURL+common.EncodeFilesystemPath(path), &body)
			require.NoError(t, err)
			request.Header.Set("Content-Type", form.FormDataContentType())
			resp, err := common.Client.Do(request)
			require.NoError(t, err)
			resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
			var file filesystem.FileWithContent
			resp, err = common.MakeRequestAndParse(http.MethodGet, common.EncodeFilesystemPath(path), nil, &file)
			require.NoError(t, err)
			resp.Body.Close()
			require.Equal(t, "777", file.Permissions, "first=%v requested=%s", first, requested)
		}
	}
}
