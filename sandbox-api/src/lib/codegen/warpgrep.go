package codegen

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// WarpGrep model and protocol constants.
const (
	// WarpGrepModel is the MorphLLM model identifier for WarpGrep.
	WarpGrepModel = "morph-warp-grep-v2.1"

	// DefaultWarpGrepMaxTurns matches MorphLLM's documented agent loop budget.
	DefaultWarpGrepMaxTurns = 6

	// DefaultWarpGrepMaxTokens is the recommended max_tokens per docs.
	DefaultWarpGrepMaxTokens = 2048

	// DefaultWarpGrepRequestTimeout caps a single chat-completion request.
	DefaultWarpGrepRequestTimeout = 60 * time.Second

	// warpGrepRepoStructureDepth controls how deep the initial repo structure
	// is enumerated (matches docs: depth 2).
	warpGrepRepoStructureDepth = 2

	// warpGrepMaxRepoStructureEntries caps the number of paths included in the
	// initial repo_structure message to avoid blowing past the API's token
	// budget on very large repositories.
	warpGrepMaxRepoStructureEntries = 500

	// Tool output truncation limits, kept generous but bounded so we never
	// stream an unbounded amount of data back into the model context.
	warpGrepMaxGrepLines = 200
	warpGrepMaxListLines = 200
	warpGrepMaxReadLines = 800
	warpGrepMaxGlobFiles = 100
)

// IsWarpGrepEnabled reports whether WarpGrep can be used. WarpGrep is only
// available via MorphLLM, so it follows MORPH_API_KEY.
func IsWarpGrepEnabled() bool {
	return os.Getenv("MORPH_API_KEY") != ""
}

// WarpGrepClient orchestrates the MorphLLM WarpGrep agentic search loop. The
// model itself runs in MorphLLM's infrastructure, but the search tools
// (`grep_search`, `read`, `list_directory`, `glob`, `finish`) are executed
// locally against the sandbox filesystem.
type WarpGrepClient struct {
	APIKey  string
	BaseURL string
	Model   string
	Client  *http.Client
}

// WarpGrepOptions tunes a single WarpGrep run.
type WarpGrepOptions struct {
	// MaxTurns caps the number of model round-trips. Defaults to
	// DefaultWarpGrepMaxTurns when zero.
	MaxTurns int

	// MaxTokens forwarded to the chat completions request. Defaults to
	// DefaultWarpGrepMaxTokens when zero.
	MaxTokens int

	// Temperature forwarded to the chat completions request. Defaults to 0.0
	// (deterministic) when nil.
	Temperature *float64
}

// WarpGrepResult is the structured outcome of a WarpGrep run.
type WarpGrepResult struct {
	// RepoRoot is the absolute repo root the search was rooted at.
	RepoRoot string `json:"repoRoot"`

	// Query is the original natural-language search string.
	Query string `json:"query"`

	// Files is the parsed list of relevant code locations as returned by the
	// model's `finish` tool call (one entry per `path[:lines]` segment).
	Files []WarpGrepFile `json:"files"`

	// Answer is the raw `files` string returned by the `finish` tool, kept for
	// callers that prefer the unparsed output.
	Answer string `json:"answer,omitempty"`

	// Turns is the number of model round-trips that were executed.
	Turns int `json:"turns"`

	// Finished is true if the model called `finish`; false if the loop ran out
	// of turns or terminated because the model produced no further tool calls.
	Finished bool `json:"finished"`
}

// WarpGrepFile is a single relevant code location returned by WarpGrep.
type WarpGrepFile struct {
	// Path is the absolute or repo-relative path to the file.
	Path string `json:"path"`

	// Lines is an optional line range hint (e.g. "1-50") as emitted by the
	// model. Empty when the model points at the whole file.
	Lines string `json:"lines,omitempty"`
}

// NewWarpGrepClient creates a new WarpGrep client.
func NewWarpGrepClient(apiKey string) *WarpGrepClient {
	return &WarpGrepClient{
		APIKey:  apiKey,
		BaseURL: "https://api.morphllm.com/v1",
		Model:   WarpGrepModel,
		Client:  &http.Client{Timeout: DefaultWarpGrepRequestTimeout},
	}
}

// ProviderName returns the provider identifier.
func (w *WarpGrepClient) ProviderName() string {
	return "morphllm-warpgrep"
}

// warpGrepToolCall mirrors the OpenAI tool_call response shape.
type warpGrepToolCall struct {
	ID       string               `json:"id"`
	Type     string               `json:"type"`
	Function warpGrepToolFunction `json:"function"`
}

type warpGrepToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// warpGrepMessage is the OpenAI-compatible chat message shape used by the
// WarpGrep API. ToolCalls is only set on assistant messages; ToolCallID is
// only set on tool messages.
type warpGrepMessage struct {
	Role       string             `json:"role"`
	Content    string             `json:"content"`
	ToolCalls  []warpGrepToolCall `json:"tool_calls,omitempty"`
	ToolCallID string             `json:"tool_call_id,omitempty"`
	Name       string             `json:"name,omitempty"`
}

type warpGrepRequest struct {
	Model       string            `json:"model"`
	Messages    []warpGrepMessage `json:"messages"`
	Temperature float64           `json:"temperature"`
	MaxTokens   int               `json:"max_tokens,omitempty"`
}

type warpGrepResponseChoice struct {
	Index        int             `json:"index"`
	Message      warpGrepMessage `json:"message"`
	FinishReason string          `json:"finish_reason"`
}

type warpGrepResponse struct {
	Choices []warpGrepResponseChoice `json:"choices"`
}

// Execute runs the WarpGrep agent loop against repoRoot for the natural
// language query.
//
// repoRoot must be an absolute or working-directory-relative path to a real
// directory on the local filesystem; all tool calls returned by the model are
// executed against the local filesystem rooted there.
func (w *WarpGrepClient) Execute(ctx context.Context, repoRoot, query string, opts *WarpGrepOptions) (*WarpGrepResult, error) {
	if opts == nil {
		opts = &WarpGrepOptions{}
	}
	if opts.MaxTurns <= 0 {
		opts.MaxTurns = DefaultWarpGrepMaxTurns
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = DefaultWarpGrepMaxTokens
	}
	temperature := 0.0
	if opts.Temperature != nil {
		temperature = *opts.Temperature
	}

	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("warpgrep: query must not be empty")
	}

	absRoot, err := filepath.Abs(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("warpgrep: failed to resolve repo root: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return nil, fmt.Errorf("warpgrep: failed to stat repo root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("warpgrep: repo root must be a directory: %s", absRoot)
	}

	structure := buildRepoStructure(absRoot, warpGrepRepoStructureDepth, warpGrepMaxRepoStructureEntries)

	initial := fmt.Sprintf(
		"<repo_structure>\n%s\n</repo_structure>\n\n<search_string>\n%s\n</search_string>",
		structure,
		strings.TrimSpace(query),
	)

	messages := []warpGrepMessage{
		{Role: "user", Content: initial},
	}

	result := &WarpGrepResult{
		RepoRoot: absRoot,
		Query:    query,
	}

	for turn := 1; turn <= opts.MaxTurns; turn++ {
		assistant, err := w.callOnce(ctx, warpGrepRequest{
			Model:       w.Model,
			Messages:    messages,
			Temperature: temperature,
			MaxTokens:   opts.MaxTokens,
		})
		if err != nil {
			return nil, err
		}

		messages = append(messages, assistant.Message)
		result.Turns = turn

		// No tool calls means the model is done (either with a final text
		// answer or because it gave up). Either way, exit the loop.
		if len(assistant.Message.ToolCalls) == 0 {
			if strings.TrimSpace(assistant.Message.Content) != "" {
				result.Answer = strings.TrimSpace(assistant.Message.Content)
			}
			return result, nil
		}

		// Execute every tool call in this turn, appending one tool message per
		// call so the next turn sees all results in order.
		var finished bool
		for _, call := range assistant.Message.ToolCalls {
			if call.Function.Name == "finish" {
				files, raw := parseFinishArguments(call.Function.Arguments)
				result.Files = files
				result.Answer = raw
				result.Finished = true
				finished = true
				// Even after finish, append a tool message so the conversation
				// stays well-formed if we ever continue it for logging.
				messages = append(messages, warpGrepMessage{
					Role:       "tool",
					ToolCallID: call.ID,
					Name:       call.Function.Name,
					Content:    "ok",
				})
				continue
			}

			output := executeWarpGrepTool(absRoot, call.Function.Name, call.Function.Arguments)
			messages = append(messages, warpGrepMessage{
				Role:       "tool",
				ToolCallID: call.ID,
				Name:       call.Function.Name,
				Content:    output,
			})
		}

		if finished {
			return result, nil
		}
	}

	// Loop budget exhausted without the model calling finish.
	return result, nil
}

func (w *WarpGrepClient) callOnce(ctx context.Context, body warpGrepRequest) (*warpGrepResponseChoice, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("warpgrep: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.BaseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("warpgrep: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+w.APIKey)

	resp, err := w.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("warpgrep: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("warpgrep: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("warpgrep: API returned %d: %s", resp.StatusCode, string(raw))
	}

	var parsed warpGrepResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("warpgrep: decode response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("warpgrep: empty choices in response")
	}
	return &parsed.Choices[0], nil
}

// parseFinishArguments parses the JSON arguments of a `finish` tool call and
// returns the structured list of files plus the raw `files` string.
func parseFinishArguments(arguments string) ([]WarpGrepFile, string) {
	var args struct {
		Files string `json:"files"`
	}
	_ = json.Unmarshal([]byte(arguments), &args)

	raw := strings.TrimSpace(args.Files)
	if raw == "" {
		return nil, ""
	}

	out := make([]WarpGrepFile, 0)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: path[:lines]. Only split on the LAST ':' so paths containing
		// colons (e.g. Windows drives) are preserved.
		idx := strings.LastIndex(line, ":")
		if idx <= 0 || idx == len(line)-1 {
			out = append(out, WarpGrepFile{Path: line})
			continue
		}
		path := line[:idx]
		lines := line[idx+1:]
		// Heuristic: only treat the suffix as a line range if it looks like
		// digits/dashes/commas. Otherwise it is probably part of the path.
		if !looksLikeLineRange(lines) {
			out = append(out, WarpGrepFile{Path: line})
			continue
		}
		out = append(out, WarpGrepFile{Path: path, Lines: lines})
	}
	return out, raw
}

func looksLikeLineRange(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r == '-' || r == ',' || r == ' ':
		default:
			return false
		}
	}
	return true
}

// buildRepoStructure enumerates the repo root as a flat list of absolute
// paths, up to the requested depth, capped at maxEntries.
func buildRepoStructure(root string, depth, maxEntries int) string {
	var lines []string
	lines = append(lines, root)

	skipDirs := map[string]struct{}{
		".git":         {},
		"node_modules": {},
		"__pycache__":  {},
		".venv":        {},
		"venv":         {},
		"dist":         {},
		"build":        {},
		".next":        {},
		"target":       {},
		"vendor":       {},
	}

	var walk func(dir string, currentDepth int)
	walk = func(dir string, currentDepth int) {
		if currentDepth > depth || len(lines) >= maxEntries {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			if len(lines) >= maxEntries {
				return
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if _, skip := skipDirs[name]; skip {
				continue
			}
			full := filepath.Join(dir, name)
			lines = append(lines, full)
			if entry.IsDir() {
				walk(full, currentDepth+1)
			}
		}
	}

	walk(root, 1)
	return strings.Join(lines, "\n")
}

// executeWarpGrepTool dispatches a tool call to the right local executor and
// returns the tool output as a plain string.
func executeWarpGrepTool(repoRoot, name, arguments string) string {
	switch name {
	case "grep_search":
		var args struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
			Glob    string `json:"glob"`
			Limit   int    `json:"limit"`
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return fmt.Sprintf("Error: invalid arguments for grep_search: %v", err)
		}
		return executeWarpGrepGrep(repoRoot, args.Pattern, args.Path, args.Glob, args.Limit)
	case "read":
		var args struct {
			Path  string `json:"path"`
			Lines string `json:"lines"`
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return fmt.Sprintf("Error: invalid arguments for read: %v", err)
		}
		return executeWarpGrepRead(repoRoot, args.Path, args.Lines)
	case "list_directory":
		var args struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return fmt.Sprintf("Error: invalid arguments for list_directory: %v", err)
		}
		return executeWarpGrepList(repoRoot, args.Command)
	case "glob":
		var args struct {
			Pattern string `json:"pattern"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal([]byte(arguments), &args); err != nil {
			return fmt.Sprintf("Error: invalid arguments for glob: %v", err)
		}
		return executeWarpGrepGlob(repoRoot, args.Pattern, args.Path)
	default:
		return fmt.Sprintf("Error: unknown tool %q", name)
	}
}

func executeWarpGrepGrep(repoRoot, pattern, path, globFilter string, limit int) string {
	if strings.TrimSpace(pattern) == "" {
		return "Error: pattern is required"
	}
	if path == "" {
		path = repoRoot
	}
	resolved, ok := resolveInsideRoot(repoRoot, path)
	if !ok {
		return fmt.Sprintf("Error: path %q is outside the search root", path)
	}

	args := []string{"--line-number", "--no-heading", "--color", "never", "-C", "1"}
	if globFilter != "" {
		args = append(args, "--glob", globFilter)
	}
	args = append(args, "--", pattern, resolved)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "rg", args...)
	out, err := cmd.Output()
	if err != nil {
		// rg exits non-zero when there are no matches; treat that as "no matches".
		if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() == 1 {
			return "no matches"
		}
		if ctx.Err() == context.DeadlineExceeded {
			return "Error: search timed out"
		}
		return fmt.Sprintf("Error: %v", err)
	}

	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "no matches"
	}

	lines := strings.Split(trimmed, "\n")
	if limit > 0 && len(lines) > limit {
		return strings.Join(lines[:limit], "\n") + fmt.Sprintf("\n... (truncated at %d lines)", limit)
	}
	if len(lines) > warpGrepMaxGrepLines {
		return strings.Join(lines[:warpGrepMaxGrepLines], "\n") + fmt.Sprintf("\n... (truncated at %d lines)", warpGrepMaxGrepLines)
	}
	return trimmed
}

func executeWarpGrepRead(repoRoot, path, lines string) string {
	if path == "" {
		return "Error: path is required"
	}
	resolved, ok := resolveInsideRoot(repoRoot, path)
	if !ok {
		return fmt.Sprintf("Error: path %q is outside the search root", path)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Sprintf("[FILE NOT FOUND] %s does not exist", path)
		}
		return fmt.Sprintf("Error: %v", err)
	}

	all := strings.Split(string(data), "\n")
	var selected []int
	if strings.TrimSpace(lines) == "" {
		for i := range all {
			selected = append(selected, i)
		}
	} else {
		selected = parseLineRanges(lines, len(all))
	}

	out := make([]string, 0, len(selected))
	seen := make(map[int]struct{}, len(selected))
	for _, idx := range selected {
		if idx < 0 || idx >= len(all) {
			continue
		}
		if _, dup := seen[idx]; dup {
			continue
		}
		seen[idx] = struct{}{}
		out = append(out, fmt.Sprintf("%d|%s", idx+1, all[idx]))
	}
	sort.Slice(out, func(i, j int) bool {
		// Lines are already in insertion order (which followed selected order);
		// final view is more useful when ascending by line number, so re-sort
		// by the leading integer.
		ai := leadingInt(out[i])
		aj := leadingInt(out[j])
		return ai < aj
	})

	if len(out) > warpGrepMaxReadLines {
		out = out[:warpGrepMaxReadLines]
		out = append(out, fmt.Sprintf("... truncated (%d total lines)", len(all)))
	}
	return strings.Join(out, "\n")
}

func leadingInt(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}

// parseLineRanges accepts "1-50" or "1-20,45-80" or "42" and returns the
// 0-indexed line indices to include.
func parseLineRanges(spec string, total int) []int {
	var out []int
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.Contains(part, "-") {
			ends := strings.SplitN(part, "-", 2)
			startStr := strings.TrimSpace(ends[0])
			endStr := strings.TrimSpace(ends[1])
			start, errA := strconv.Atoi(startStr)
			end, errB := strconv.Atoi(endStr)
			if errA != nil || errB != nil || start < 1 || end < start {
				continue
			}
			if end > total {
				end = total
			}
			for i := start; i <= end; i++ {
				out = append(out, i-1)
			}
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 1 || n > total {
			continue
		}
		out = append(out, n-1)
	}
	return out
}

// executeWarpGrepList implements the `list_directory` tool. The model passes a
// shell-style command (typically `ls <path>` or `find <path>`); we extract the
// target directory and walk it ourselves rather than shelling out, so the
// output stays bounded and consistent regardless of the host environment.
func executeWarpGrepList(repoRoot, command string) string {
	dir := repoRoot
	tokens := strings.Fields(command)
	for _, tok := range tokens[1:] {
		if strings.HasPrefix(tok, "-") || strings.HasPrefix(tok, "|") {
			continue
		}
		dir = tok
		break
	}
	resolved, ok := resolveInsideRoot(repoRoot, dir)
	if !ok {
		return fmt.Sprintf("Error: directory %q is outside the search root", dir)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Sprintf("Error: directory not found: %s", dir)
	}
	if !info.IsDir() {
		return fmt.Sprintf("Error: %s is not a directory", dir)
	}

	skip := map[string]struct{}{
		".git": {}, "node_modules": {}, "__pycache__": {}, ".venv": {},
		"venv": {}, "dist": {}, "build": {}, ".next": {},
	}

	var lines []string
	var walk func(p string, depth int)
	walk = func(p string, depth int) {
		if depth > 3 || len(lines) >= warpGrepMaxListLines {
			return
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			if len(lines) >= warpGrepMaxListLines {
				return
			}
			name := entry.Name()
			if strings.HasPrefix(name, ".") {
				continue
			}
			if _, skipped := skip[name]; skipped {
				continue
			}
			indent := strings.Repeat("  ", depth)
			suffix := ""
			if entry.IsDir() {
				suffix = "/"
			}
			lines = append(lines, fmt.Sprintf("%s%s%s", indent, name, suffix))
			if entry.IsDir() {
				walk(filepath.Join(p, name), depth+1)
			}
		}
	}

	walk(resolved, 0)
	if len(lines) == 0 {
		return "(empty)"
	}
	return strings.Join(lines, "\n")
}

func executeWarpGrepGlob(repoRoot, pattern, path string) string {
	if strings.TrimSpace(pattern) == "" {
		return "Error: pattern is required"
	}
	searchDir := repoRoot
	if path != "" {
		resolved, ok := resolveInsideRoot(repoRoot, path)
		if !ok {
			return fmt.Sprintf("Error: path %q is outside the search root", path)
		}
		searchDir = resolved
	}

	info, err := os.Stat(searchDir)
	if err != nil || !info.IsDir() {
		return fmt.Sprintf("Error: directory not found: %s", searchDir)
	}

	skip := map[string]struct{}{
		".git": {}, "node_modules": {}, "__pycache__": {}, ".venv": {},
		"venv": {}, "dist": {}, "build": {},
	}

	type entry struct {
		path  string
		mtime time.Time
	}
	var matches []entry

	err = filepath.Walk(searchDir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if _, skipped := skip[info.Name()]; skipped {
				return filepath.SkipDir
			}
			return nil
		}
		ok, err := filepath.Match(pattern, info.Name())
		if err == nil && ok {
			matches = append(matches, entry{p, info.ModTime()})
			return nil
		}
		// Also match against the path relative to searchDir, so patterns like
		// "src/**/*.go" still hit (filepath.Match doesn't support **, but the
		// simple case of "subdir/pattern" still works for "subdir/file.ext").
		rel, relErr := filepath.Rel(searchDir, p)
		if relErr == nil {
			if matched, _ := filepath.Match(pattern, rel); matched {
				matches = append(matches, entry{p, info.ModTime()})
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Sprintf("Error: %v", err)
	}

	if len(matches) == 0 {
		return "no matches"
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].mtime.After(matches[j].mtime)
	})
	if len(matches) > warpGrepMaxGlobFiles {
		matches = matches[:warpGrepMaxGlobFiles]
	}

	paths := make([]string, len(matches))
	for i, m := range matches {
		paths[i] = m.path
	}
	header := fmt.Sprintf("Found %d file(s) matching %q within %s, sorted by modification time (newest first):", len(paths), pattern, searchDir)
	return header + "\n---\n" + strings.Join(paths, "\n") + "\n---"
}

// resolveInsideRoot interprets path as either absolute or relative to
// repoRoot and returns the cleaned absolute path along with true if it is
// inside repoRoot. Paths outside the root are rejected.
func resolveInsideRoot(repoRoot, path string) (string, bool) {
	if path == "" {
		return repoRoot, true
	}
	candidate := path
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(repoRoot, candidate)
	}
	candidate = filepath.Clean(candidate)
	cleanRoot := filepath.Clean(repoRoot)
	if candidate == cleanRoot {
		return candidate, true
	}
	if !strings.HasPrefix(candidate, cleanRoot+string(filepath.Separator)) {
		return "", false
	}
	return candidate, true
}
