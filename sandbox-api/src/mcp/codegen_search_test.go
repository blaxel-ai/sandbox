package mcp

import (
	"context"
	stdjson "encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/blaxel-ai/sandbox-api/src/handler"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestCodegenFileSearchReturnsEmptyArrayForNonmatchingFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WORKDIR", dir)
	if err := os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("ordinary content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	s := &Server{mcpServer: protocol.NewServer(&protocol.Implementation{Name: "test", Version: "1"}, nil), handlers: &Handlers{FileSystem: handler.NewFileSystemHandler()}}
	if err := s.registerCodegenTools(); err != nil {
		t.Fatal(err)
	}
	ct, st := protocol.NewInMemoryTransports()
	ss, err := s.mcpServer.Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	cs, err := protocol.NewClient(&protocol.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	result, err := cs.CallTool(ctx, &protocol.CallToolParams{Name: "codegenFileSearch", Arguments: map[string]any{"query": "nothing-matches-this"}})
	if err != nil {
		t.Fatal(err)
	}
	if result.IsError {
		t.Fatalf("tool failed: %+v", result.Content)
	}
	raw, err := stdjson.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]stdjson.RawMessage
	if err := stdjson.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if got := string(body["matches"]); got != "[]" {
		t.Fatalf("matches = %s, want []", got)
	}
}
