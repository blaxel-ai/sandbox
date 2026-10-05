package handler

import (
	"reflect"
	"testing"
)

func TestContentSearchLineCancellation(t *testing.T) {
	stopped := make(chan struct{})
	var visited []string
	visitContentSearchLines([]string{"needle", "nonmatching", "more nonmatching"}, stopped, func(number int, line string) {
		if number != 0 {
			t.Errorf("visited line %d after result limit", number)
		}
		visited = append(visited, line)
		close(stopped)
	})
	if !reflect.DeepEqual(visited, []string{"needle"}) {
		t.Fatalf("visited %v", visited)
	}
	visitContentSearchLines([]string{"another file"}, stopped, func(int, string) { t.Fatal("visited already cancelled content") })
}

func TestContentSearchLineIterationPreservesLines(t *testing.T) {
	var lines []string
	visitContentSearchLines([]string{"a", "", "b", ""}, make(chan struct{}), func(number int, line string) {
		if number != len(lines) {
			t.Fatalf("line number %d want %d", number, len(lines))
		}
		lines = append(lines, line)
	})
	if !reflect.DeepEqual(lines, []string{"a", "", "b", ""}) {
		t.Fatalf("lines = %v", lines)
	}
}
