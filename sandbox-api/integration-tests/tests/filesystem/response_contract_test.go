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

func TestFuzzySearchUsesQueryParam(t *testing.T) {
	dir := uniqueTestDir("fs-fuzzy")
	writeTestFile(t, dir+"/main.go", "package main")
	writeTestFile(t, dir+"/readme.md", "hi")
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})

	resp, err := common.MakeRequest(http.MethodGet, encodeRoute("/filesystem-search", dir), nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var body struct {
		Matches []struct {
			Path string `json:"path"`
		} `json:"matches"`
	}
	resp, err = common.MakeRequestAndParse(http.MethodGet, encodeRoute("/filesystem-search", dir)+"?query=mngo", nil, &body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, body.Matches, 1)
	require.Equal(t, "main.go", body.Matches[0].Path)
}

func TestContentSearchReturnsAtMaxResults(t *testing.T) {
	dir := uniqueTestDir("fs-max-results")
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})
	for _, name := range []string{"a.txt", "b.txt"} {
		writeTestFile(t, dir+"/"+name, strings.Repeat("needle\n", 500))
	}
	var body struct {
		Matches []struct {
			Path string `json:"path"`
		} `json:"matches"`
		Total int `json:"total"`
	}
	resp, err := common.MakeRequestAndParse(http.MethodGet, encodeRoute("/filesystem-content-search", dir)+"?query=needle", nil, &body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, body.Matches, 100)
	require.Equal(t, 100, body.Total)
}

func TestContentSearchFillsContextLines(t *testing.T) {
	dir := uniqueTestDir("fs-context")
	writeTestFile(t, dir+"/a.txt", "one\ntwo\nthree needle\nfour\nfive\n")
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})

	var body struct {
		Matches []struct {
			Text    string `json:"text"`
			Context string `json:"context"`
		} `json:"matches"`
	}
	resp, err := common.MakeRequestAndParse(http.MethodGet, encodeRoute("/filesystem-content-search", dir)+"?query=needle&contextLines=1", nil, &body)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, body.Matches, 1)
	require.Equal(t, "two\nthree needle\nfour", body.Matches[0].Context)
}

func TestHeadFilesystemReturnsStatHeaders(t *testing.T) {
	dir := uniqueTestDir("fs-stat")
	path := dir + "/hello.txt"
	writeTestFile(t, path, "hello")
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})

	resp, err := common.MakeRequest(http.MethodHead, common.EncodeFilesystemPath(path), nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "5", resp.Header.Get("Content-Length"))
	require.Equal(t, "file", resp.Header.Get("X-File-Type"))
	require.NotEmpty(t, resp.Header.Get("X-File-Mode"))
	_, err = http.ParseTime(resp.Header.Get("Last-Modified"))
	require.NoError(t, err)

	resp, err = common.MakeRequest(http.MethodHead, common.EncodeFilesystemPath(dir), nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "directory", resp.Header.Get("X-File-Type"))

	resp, err = common.MakeRequest(http.MethodHead, common.EncodeFilesystemPath(dir+"/missing"), nil)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Empty(t, resp.Header.Get("X-File-Type"))
}

func TestHeadFilesystemSpecialModesAndUnreadableContent(t *testing.T) {
	dir := uniqueTestDir("fs-head-modes")
	path := dir + "/file"
	writeTestFile(t, path, "hello")
	t.Cleanup(func() {
		resp, err := common.MakeRequest(http.MethodDelete, common.EncodeFilesystemPath(dir)+"?recursive=true", nil)
		if err == nil {
			resp.Body.Close()
		}
	})
	for _, tc := range []struct{ path, mode, kind string }{
		{dir, "1777", "directory"}, {path, "4755", "file"},
		{path, "2755", "file"}, {path, "0", "file"},
	} {
		var result struct {
			ExitCode int `json:"exitCode"`
		}
		resp, err := common.MakeRequestAndParse(http.MethodPost, "/process", map[string]interface{}{
			"command":           fmt.Sprintf("chmod %s %s", tc.mode, tc.path),
			"waitForCompletion": true,
		}, &result)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, 0, result.ExitCode)
		resp, err = common.MakeRequest(http.MethodHead, common.EncodeFilesystemPath(tc.path), nil)
		require.NoError(t, err)
		resp.Body.Close()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		require.Equal(t, tc.mode, resp.Header.Get("X-File-Mode"))
		require.Equal(t, tc.kind, resp.Header.Get("X-File-Type"))
	}
}
