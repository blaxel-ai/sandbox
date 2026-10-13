package handler

import (
	"errors"
	"testing"

	"github.com/basetenlabs/sandbox/sandbox-api/src/handler/process"
	"github.com/basetenlabs/sandbox/sandbox-api/src/lib/scale"
)

func useTempLogDir(t *testing.T) {
	t.Helper()
	old := process.ProcessLogDir
	process.ProcessLogDir = t.TempDir()
	t.Cleanup(func() { process.ProcessLogDir = old })
}

func TestExecuteProcessKeepAliveDisabled(t *testing.T) {
	useTempLogDir(t)
	t.Setenv(scale.EnvDisableKeepAlive, "true")
	h := NewProcessHandler()

	_, err := h.ExecuteProcess("echo keepalive", "", "", nil, true, 5, nil, false, 0, true, false)
	if !errors.Is(err, ErrKeepAliveDisabled) {
		t.Fatalf("ExecuteProcess with keepAlive=true should return ErrKeepAliveDisabled, got %v", err)
	}

	// keepAlive=false still works
	resp, err := h.ExecuteProcess("echo keepalive", "", "", nil, true, 5, nil, false, 0, false, false)
	if err != nil {
		t.Fatalf("ExecuteProcess with keepAlive=false should succeed, got %v", err)
	}
	if resp.PID == "" {
		t.Fatal("expected a PID for the non-keepAlive process")
	}
}

func TestExecuteProcessKeepAliveEnabledByDefault(t *testing.T) {
	useTempLogDir(t)
	t.Setenv(scale.EnvDisableKeepAlive, "")
	h := NewProcessHandler()

	resp, err := h.ExecuteProcess("echo keepalive", "", "", nil, true, 5, nil, false, 0, true, false)
	if err != nil {
		t.Fatalf("ExecuteProcess with keepAlive=true should succeed when the flag is unset, got %v", err)
	}
	if resp.PID == "" {
		t.Fatal("expected a PID for the keepAlive process")
	}
}
