package codegen

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNewRelaceClientDefaultBaseURL(t *testing.T) {
	t.Setenv("RELACE_BASE_URL", "")

	client := NewRelaceClient("key")
	if client.BaseURL != defaultRelaceBaseURL {
		t.Fatalf("expected default base URL %q, got %q", defaultRelaceBaseURL, client.BaseURL)
	}
}

func TestNewRelaceClientBaseURLOverride(t *testing.T) {
	t.Setenv("RELACE_BASE_URL", "https://api.relace.run/")

	client := NewRelaceClient("key")
	if client.BaseURL != "https://api.relace.run" {
		t.Fatalf("expected trimmed override base URL, got %q", client.BaseURL)
	}
}

func TestApplyCodeEditModelDefaulting(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "empty defaults to relace-apply-3", input: "", want: relaceApplyModel},
		{name: "legacy auto coerced to relace-apply-3", input: "auto", want: relaceApplyModel},
		{name: "explicit model preserved", input: "relace-apply-3", want: "relace-apply-3"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath, gotModel string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				body, _ := io.ReadAll(r.Body)
				var req RelaceApplyRequest
				if err := json.Unmarshal(body, &req); err != nil {
					t.Fatalf("failed to unmarshal request: %v", err)
				}
				gotModel = req.Model
				_, _ = w.Write([]byte(`{"mergedCode":"merged"}`))
			}))
			defer server.Close()

			client := NewRelaceClient("key")
			client.BaseURL = server.URL

			merged, err := client.ApplyCodeEdit("original", "edit", tc.input)
			if err != nil {
				t.Fatalf("ApplyCodeEdit returned error: %v", err)
			}
			if merged != "merged" {
				t.Fatalf("expected merged code %q, got %q", "merged", merged)
			}
			if gotPath != "/v1/code/apply" {
				t.Fatalf("expected apply path /v1/code/apply, got %q", gotPath)
			}
			if gotModel != tc.want {
				t.Fatalf("expected model %q, got %q", tc.want, gotModel)
			}
		})
	}
}

func TestRerankCodeUsesConfiguredBaseURL(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"results":[{"filename":"a.go","score":0.9}]}`))
	}))
	defer server.Close()

	client := NewRelaceClient("key")
	client.BaseURL = server.URL

	ranked, err := client.RerankCode([]CodebaseDocument{{Path: "a.go", Content: "package a"}}, "query", 100)
	if err != nil {
		t.Fatalf("RerankCode returned error: %v", err)
	}
	if gotPath != "/v2/code/rank" {
		t.Fatalf("expected rerank path /v2/code/rank, got %q", gotPath)
	}
	if len(ranked) != 1 || ranked[0].Path != "a.go" {
		t.Fatalf("unexpected ranked result: %+v", ranked)
	}
}
