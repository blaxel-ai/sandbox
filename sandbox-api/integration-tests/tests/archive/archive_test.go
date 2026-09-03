package tests

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func archiveStatus(t *testing.T) map[string]interface{} {
	t.Helper()
	resp, err := common.MakeRequest(http.MethodGet, "/archive/status", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var status map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	return status
}

func resumeArchive(t *testing.T) {
	t.Helper()
	resp, err := common.MakeRequest(http.MethodPost, "/archive/resume", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestArchiveStatus checks the status a sandbox reports when no export ever ran,
// which is what the control plane polls to know a sandbox is still serving.
func TestArchiveStatus(t *testing.T) {
	status := archiveStatus(t)
	assert.Equal(t, "active", status["state"])
	assert.NotContains(t, status, "since")
}

// TestArchiveExportRequiresURL checks the export is refused as a bad request,
// not attempted, when there is nowhere to upload to.
func TestArchiveExportRequiresURL(t *testing.T) {
	resp, err := common.MakeRequest(http.MethodPost, "/archive/export", map[string]interface{}{})
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "active", archiveStatus(t)["state"],
		"a refused export must not stop the sandbox")
}

// TestArchiveExportInvalidJSON checks a malformed body is rejected before
// anything is stopped.
func TestArchiveExportInvalidJSON(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, common.BaseURL+"/archive/export", strings.NewReader(`{"dryRun":`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := common.Client.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, "active", archiveStatus(t)["state"])
}

// TestArchiveExportDryRunKeepsTheSandboxServing checks a dry run reports the
// archive it would produce without freezing anything, which is what makes it
// safe to call on a live sandbox. It compares the root against itself, so the
// interesting part is the state afterwards, not the (empty) diff.
func TestArchiveExportDryRunKeepsTheSandboxServing(t *testing.T) {
	exportRequest := map[string]interface{}{
		"dryRun":          true,
		"imageMountPoint": "/",
	}
	resp, err := common.MakeRequestWithTimeout(http.MethodPost, "/archive/export", exportRequest, 90*time.Second)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&result))
	assert.Equal(t, false, result["uploaded"], "a dry run must not upload")
	assert.NotNil(t, result["size"], "a dry run reports the size the archive would have")
	assert.Nil(t, result["stoppedProcesses"], "a dry run must not stop the workload")

	assert.Equal(t, "active", archiveStatus(t)["state"])
}

// TestArchiveExportFailureDoesNotLockTheSandbox is the important one: the export
// freezes the sandbox before reading the filesystem, so a failure has to lift
// the freeze again. Otherwise a sandbox whose export failed would answer 503 to
// every call for the rest of its life.
//
// It is opt-in because it stops the workload and answers 503 to every other
// endpoint while it runs, and `go test ./...` runs the test packages against a
// single sandbox in parallel.
func TestArchiveExportFailureDoesNotLockTheSandbox(t *testing.T) {
	if os.Getenv("ARCHIVE_QUIESCE_TESTS") != "true" {
		t.Skip("freezes the whole sandbox; run with ARCHIVE_QUIESCE_TESTS=true and -p 1")
	}
	defer resumeArchive(t)

	processRequest := map[string]interface{}{
		"name":    "archive-export-target",
		"command": "sleep 300",
	}
	resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// A device that cannot be mounted makes the export fail after the sandbox
	// is frozen and the processes are stopped.
	exportRequest := map[string]interface{}{
		"url":         "http://127.0.0.1:1/never-reached",
		"imageDevice": "/dev/blaxel-archive-does-not-exist",
	}
	resp, err = common.MakeRequestWithTimeout(http.MethodPost, "/archive/export", exportRequest, 90*time.Second)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	assert.Equal(t, "active", archiveStatus(t)["state"],
		"a failed export must leave the sandbox serving calls again")

	// The API answers, and the process stopped to freeze the filesystem is
	// reported as stopped rather than silently restarted.
	resp, err = common.MakeRequest(http.MethodGet, "/process/archive-export-target", nil)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var details map[string]interface{}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&details))
	assert.NotEqual(t, "running", details["status"],
		"the export must have stopped the process before reading the filesystem")
}

// TestArchiveResumeIsIdempotent checks resume on a sandbox that was never frozen
// is a no-op, so a control plane can call it without tracking state.
func TestArchiveResumeIsIdempotent(t *testing.T) {
	resumeArchive(t)
	resumeArchive(t)
	assert.Equal(t, "active", archiveStatus(t)["state"])
}
