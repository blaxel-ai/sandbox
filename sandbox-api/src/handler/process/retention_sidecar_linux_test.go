//go:build linux

package process

import (
	"bytes"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func withoutLogXattrs(t *testing.T) {
	t.Helper()
	original := retentionIO.Load()
	calls := *original
	calls.getXattr = func(int, string, []byte) (int, error) { return -1, unix.ENOTSUP }
	calls.setXattr = func(int, string, []byte, int) error { return unix.ENOTSUP }
	retentionIO.Store(&calls)
	t.Cleanup(func() { retentionIO.Store(original) })
}

// Exercise retention, collection, and replay with simulated ENOTSUP responses.
func TestRetentionWithoutExtendedAttributes(t *testing.T) {
	withoutLogXattrs(t)
	for _, tc := range []struct {
		name string
		run  func(*testing.T)
	}{
		{"collector", TestRetentionOvertakesCollectorExplicitly},
		{"replay", TestReplayRetentionDuringDeliveryIsExplicit},
		{"reopen", TestRetentionBoundarySurvivesReopenAndLargerBudget},
		{"legacy", TestRetentionRecognizesLegacyHoleWithoutDroppingNULs},
		{"published", TestRetentionPublishedBeforePunchIsConservative},
		{"retry", TestRetentionRetriesPublishedBoundary},
		{"recovery", TestRetentionGapPersistsWithoutStateSnapshot},
		{"ownership", TestStructuredCollectorOwnershipAndTakeover},
		{"sparse", TestNewLogPreservesIntentionalSparseNULs},
		{"nul", TestLogTailPreservesWrittenNULs},
		{"failed-start", TestStructuredFailedStartRemovesAllLogFiles},
		{"partial-creation", TestStructuredPartialLogCreationRemovesOnlyCreatedFiles},
	} {
		t.Run(tc.name, tc.run)
	}
}

func TestSidecarReclaimsPhysicalPages(t *testing.T) {
	withoutLogXattrs(t)
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "2097152")
	path := filepath.Join(t.TempDir(), "log")
	files, err := openProcessLogFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte{0, 1, 2, 3}, 2*1024*1024)
	if _, err := files[0].Write(data); err != nil {
		t.Fatal(err)
	}
	files[0].Close()
	before := diskUsage(t, path)
	capLogFile(path, 1)
	after := diskUsage(t, path)
	if before != 8*1024*1024 || after != 2*1024*1024 {
		t.Fatalf("physical bytes before=%d after=%d", before, after)
	}
	tail, ok := readLogTail(path, int64(len(data)))
	if !ok || tail != truncationMarker+string(data[6*1024*1024:]) {
		t.Fatal("retained suffix changed")
	}
}

func TestSidecarReplacedFileAndRecycledIdentity(t *testing.T) {
	withoutLogXattrs(t)
	path := filepath.Join(t.TempDir(), "log")
	files, err := openProcessLogFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	old := files[0]
	defer old.Close()
	if err := recordRetainedHead(old, 4096); err != nil {
		t.Fatal(err)
	}
	oldMetadata, err := os.ReadFile(retentionSidecarPath(old))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	replacement, err := openProcessLogFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	fresh := replacement[0]
	defer fresh.Close()
	if _, err := fresh.WriteAt([]byte{1}, 8192); err != nil {
		t.Fatal(err)
	}
	if err := recordRetainedHead(old, 8192); err == nil {
		t.Fatal("unlinked descriptor published for replacement")
	}
	removeLogRetention(old)
	if head, err := retainedHead(fresh); err != nil || head != 0 {
		t.Fatalf("replacement lost zero marker: %d %v", head, err)
	}
	if err := os.WriteFile(retentionSidecarPath(fresh), oldMetadata, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := retainedHead(fresh); err == nil {
		t.Fatal("stale inode metadata accepted")
	}
	// Simulate inode reuse. Initialization must reset an old boundary even when
	// the recorded device and inode match the new file.
	if err := writeRetentionSidecar(fresh, 4096); err != nil {
		t.Fatal(err)
	}
	if err := initializeLogRetention(fresh); err != nil {
		t.Fatal(err)
	}
	if head, err := retainedHead(fresh); err != nil || head != 0 {
		t.Fatalf("stale boundary not cleared: %d %v", head, err)
	}
}

func TestSidecarPublicationFailureDoesNotPunch(t *testing.T) {
	withoutLogXattrs(t)
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "4096")
	path := filepath.Join(t.TempDir(), "log")
	files, err := openProcessLogFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	f := files[0]
	defer f.Close()
	data := bytes.Repeat([]byte{1}, 8*pageSize)
	if _, err := f.Write(data); err != nil {
		t.Fatal(err)
	}
	before := diskUsage(t, path)
	original := retentionIO.Load()
	calls := *original
	calls.rename = func(string, string) error { return unix.EIO }
	retentionIO.Store(&calls)
	t.Cleanup(func() { retentionIO.Store(original) })
	capLogFile(path, 1)
	if after := diskUsage(t, path); after != before {
		t.Fatal("punched despite failed metadata publication")
	}
	if head, err := retainedHead(f); err != nil || head != 0 {
		t.Fatalf("failed publish replaced valid metadata: %d %v", head, err)
	}
	temps, _ := filepath.Glob(path + ".retention.tmp")
	if len(temps) != 0 {
		t.Fatal("failed publication leaked temporary metadata")
	}
	retentionIO.Store(original)
	capLogFile(path, 1)
	if after := diskUsage(t, path); after >= before {
		t.Fatal("cap did not recover after publication failure")
	}
}

func TestSidecarCleanupAndMalformedMetadata(t *testing.T) {
	withoutLogXattrs(t)
	path := filepath.Join(t.TempDir(), "log")
	files, err := openProcessLogFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	sidecar := retentionSidecarPath(files[0])
	if err := os.WriteFile(sidecar, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := retainedHead(files[0]); err == nil {
		t.Fatal("malformed metadata silently ignored")
	}
	if err := initializeLogRetention(files[0]); err != nil {
		t.Fatal(err)
	}
	cleanupProcessLogFiles(files, true)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("owned log left behind")
	}
	if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
		t.Fatal("owned retention sidecar left behind")
	}
}

func TestSidecarNewManagerRecoversCappedSource(t *testing.T) {
	withoutLogXattrs(t)
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "65536")
	t.Setenv("SANDBOX_STATE_FILE", filepath.Join(t.TempDir(), "state.json"))
	pm, p, _ := structuredFixture(t)
	p.Status = StatusRunning
	p.ProcessPid = -1
	p.StdoutFile = filepath.Join(t.TempDir(), "stdout")
	p.StderrFile = filepath.Join(t.TempDir(), "stderr")
	files, err := openProcessLogFiles(p.StdoutFile, p.StderrFile)
	if err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte{0, 1, 2, 255}, 32768)
	if _, err := files[0].Write(data); err != nil {
		t.Fatal(err)
	}
	cleanupProcessLogFiles(files, false)
	if err := pm.SaveState(); err != nil {
		t.Fatal(err)
	}
	capLogFile(p.StdoutFile, 1)
	restored := NewProcessManager()
	if err := restored.LoadState(); err != nil {
		t.Fatal(err)
	}
	got := restored.processes[p.PID]
	select {
	case <-got.Finished:
	case <-time.After(3 * time.Second):
		t.Fatal("restored collector did not finish")
	}
	if !got.logIncomplete || got.stdout.Len() != len(data) {
		t.Fatal("restored collector lost gap or absolute cursor")
	}
	w := &structuredCapture{}
	if err := restored.StreamProcessOutputForProcess(got, w); err != nil {
		t.Fatal(err)
	}
	var actual []byte
	gap := false
	for _, event := range w.events {
		if event.Type == "truncated" {
			gap = true
		}
		if event.Type == "stdout" {
			actual = append(actual, []byte(event.Data)...)
		}
	}
	if !gap || len(actual) == 0 || len(actual) >= len(data) || !bytes.Equal(actual, data[len(data)-len(actual):]) {
		t.Fatal("new manager fabricated retained output")
	}
}

func TestSidecarRecoversAbandonedTemporaryFile(t *testing.T) {
	withoutLogXattrs(t)
	t.Setenv("SANDBOX_MAX_LOG_FILE_BYTES", "4096")
	path := filepath.Join(t.TempDir(), "log")
	files, err := openProcessLogFiles(path)
	if err != nil {
		t.Fatal(err)
	}
	f := files[0]
	defer f.Close()
	if _, err := f.Write(bytes.Repeat([]byte{1}, 8*pageSize)); err != nil {
		t.Fatal(err)
	}
	temp := retentionSidecarPath(f) + ".tmp"
	if err := os.WriteFile(temp, []byte("interrupted write"), 0600); err != nil {
		t.Fatal(err)
	}
	capLogFile(path, 1)
	if diskUsage(t, path) != 4096 {
		t.Fatal("abandoned metadata blocked reclamation")
	}
	if _, err := os.Stat(temp); !os.IsNotExist(err) {
		t.Fatal("abandoned temporary metadata not reclaimed")
	}
}
