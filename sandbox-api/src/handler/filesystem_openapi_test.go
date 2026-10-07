package handler

import (
	"os"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestFilesystemOpenAPIResponseSchemas(t *testing.T) {
	data, err := os.ReadFile("../../docs/openapi.yml")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]struct {
			Responses map[string]struct {
				Content map[string]struct{ Schema map[string]any }
			}
		}
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	directory := map[string]any{"$ref": "#/components/schemas/Directory"}
	for _, tc := range []struct {
		name, path, method, media string
		want                      map[string]any
	}{
		{"write tree", "/filesystem/tree/{path}", "put", "application/json", directory},
		{"read tree", "/filesystem/tree/{path}", "get", "application/json", directory},
		{"read file or directory JSON", "/filesystem/{path}", "get", "application/json", map[string]any{"oneOf": []any{directory, map[string]any{"$ref": "#/components/schemas/FileWithContent"}}}},
		{"download file", "/filesystem/{path}", "get", "application/octet-stream", map[string]any{"type": "string", "format": "binary"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := document.Paths[tc.path][tc.method].Responses["200"].Content[tc.media].Schema
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("%s %s %s success schema = %#v; want %#v", tc.method, tc.path, tc.media, got, tc.want)
			}
		})
	}
}
