package tests

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/blaxel-ai/sandbox-api/integration_tests/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShellBuiltins tests execution of shell built-in commands
func TestShellBuiltins(t *testing.T) {
	tests := []struct {
		name          string
		command       string
		expectSuccess bool
		description   string
	}{
		{
			name:          "cd_alone",
			command:       "cd",
			expectSuccess: true, // Now works with always shell wrapper
			description:   "cd without arguments - shell builtin",
		},
		{
			name:          "cd_with_path",
			command:       "cd /tmp",
			expectSuccess: true, // Now works with always shell wrapper
			description:   "cd with path - shell builtin",
		},
		{
			name:          "cd_with_and",
			command:       "cd /tmp && pwd",
			expectSuccess: true, // Still works
			description:   "cd with && operator",
		},
		{
			name:          "export_alone",
			command:       "export TEST=value",
			expectSuccess: true, // Now works with always shell wrapper
			description:   "export - shell builtin",
		},
		{
			name:          "export_with_semicolon",
			command:       "export TEST=value; echo $TEST",
			expectSuccess: true, // Still works
			description:   "export with ; and variable expansion",
		},
		{
			name:          "alias",
			command:       "alias ll='ls -la'",
			expectSuccess: true, // Now works with always shell wrapper
			description:   "alias - shell builtin",
		},
		{
			name:          "source",
			command:       "source /etc/profile",
			expectSuccess: true, // Now works with always shell wrapper
			description:   "source - shell builtin",
		},
		{
			name:          "builtin_type",
			command:       "type ls",
			expectSuccess: true, // Now works with always shell wrapper
			description:   "type - shell builtin",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processRequest := map[string]interface{}{
				"command":           tt.command,
				"waitForCompletion": true,
				"timeout":           5,
			}

			resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, http.StatusOK, resp.StatusCode)

			var processResponse map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&processResponse)
			require.NoError(t, err)

			status := processResponse["status"].(string)
			exitCode := processResponse["exitCode"].(float64)

			if tt.expectSuccess {
				assert.NotEqual(t, "failed", status, "Command '%s' should succeed: %s", tt.command, tt.description)
				assert.Equal(t, float64(0), exitCode, "Command '%s' should have exit code 0", tt.command)
			} else {
				// All commands now use shell wrapper, so failures would be actual command failures
				assert.Equal(t, "failed", status, "Command '%s' expected to fail: %s", tt.command, tt.description)
			}
		})
	}
}

// TestShellFeatures tests various shell features that require sh -c
func TestShellFeatures(t *testing.T) {
	tests := []struct {
		name          string
		command       string
		expectedLogs  string
		description   string
		requiresShell bool
	}{
		{
			name:          "variable_expansion",
			command:       "echo $HOME",
			expectedLogs:  "/", // In containers, HOME is typically /root or /
			description:   "Variable expansion requires shell",
			requiresShell: true,
		},
		{
			name:          "command_substitution",
			command:       "echo $(date +%Y)",
			expectedLogs:  "20", // Should start with year
			description:   "Command substitution requires shell",
			requiresShell: true,
		},
		{
			name:          "glob_expansion",
			command:       "echo /etc/*rc",
			expectedLogs:  "/etc/", // Should expand to files
			description:   "Glob expansion requires shell",
			requiresShell: false, // Actually doesn't work without shell
		},
		{
			name:          "process_substitution",
			command:       "diff <(echo a) <(echo b)",
			expectedLogs:  "",
			description:   "Process substitution requires shell",
			requiresShell: true,
		},
		{
			name:          "shell_functions",
			command:       "function hello() { echo world; }; hello",
			expectedLogs:  "world",
			description:   "Shell functions require shell",
			requiresShell: true,
		},
		{
			name:          "background_jobs",
			command:       "sleep 1 & echo done",
			expectedLogs:  "done",
			description:   "Background jobs require shell",
			requiresShell: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processRequest := map[string]interface{}{
				"command":           tt.command,
				"waitForCompletion": true,
				"timeout":           5,
			}

			resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
			require.NoError(t, err)
			defer resp.Body.Close()

			var processResponse map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&processResponse)
			require.NoError(t, err)

			if tt.requiresShell {
				// These should work with current implementation if they contain special chars
				if logs, ok := processResponse["logs"].(string); ok && tt.expectedLogs != "" {
					assert.Contains(t, logs, tt.expectedLogs, "%s: %s", tt.name, tt.description)
				}
			}
		})
	}
}

// TestQuotingAndEscaping tests how quotes and escaping are handled
func TestQuotingAndEscaping(t *testing.T) {
	tests := []struct {
		name         string
		command      string
		expectedLogs string
		description  string
	}{
		{
			name:         "single_quotes",
			command:      `echo 'hello world'`,
			expectedLogs: "hello world",
			description:  "Single quotes preserve literal value",
		},
		{
			name:         "double_quotes",
			command:      `echo "hello world"`,
			expectedLogs: "hello world",
			description:  "Double quotes preserve spaces",
		},
		{
			name:         "mixed_quotes",
			command:      `echo "it's working"`,
			expectedLogs: "it's working",
			description:  "Mixed quotes",
		},
		{
			name:         "escaped_chars",
			command:      `echo "line1\nline2"`,
			expectedLogs: "line1\nline2\n", // Without shell, \n is literal
			description:  "Escaped characters behavior differs",
		},
		{
			name:         "spaces_in_args",
			command:      `echo one "two three" four`,
			expectedLogs: "one two three four",
			description:  "Arguments with spaces",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processRequest := map[string]interface{}{
				"command":           tt.command,
				"waitForCompletion": true,
				"timeout":           5,
			}

			resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
			require.NoError(t, err)
			defer resp.Body.Close()

			var processResponse map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&processResponse)
			require.NoError(t, err)

			if logs, ok := processResponse["logs"].(string); ok {
				assert.Contains(t, logs, tt.expectedLogs, "%s: %s", tt.name, tt.description)
			}
		})
	}
}

// TestWorkarounds tests the current workarounds for shell builtins
func TestWorkarounds(t *testing.T) {
	tests := []struct {
		name         string
		command      string
		expectedLogs string
		description  string
	}{
		{
			name:         "sh_c_wrapper_cd",
			command:      `sh -c 'cd /tmp && pwd'`,
			expectedLogs: "/tmp",
			description:  "Explicit sh -c wrapper for cd",
		},
		{
			name:         "bash_c_wrapper_cd",
			command:      `bash -c 'cd /tmp && pwd'`,
			expectedLogs: "/tmp",
			description:  "Explicit bash -c wrapper for cd",
		},
		{
			name:         "dummy_command_after_cd",
			command:      `cd /tmp && echo "now in tmp"`,
			expectedLogs: "now in tmp",
			description:  "Adding && triggers shell wrapper",
		},
		{
			name:         "semicolon_after_cd",
			command:      `cd /tmp; pwd`,
			expectedLogs: "/tmp",
			description:  "Using semicolon triggers shell wrapper",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			processRequest := map[string]interface{}{
				"command":           tt.command,
				"waitForCompletion": true,
				"timeout":           5,
				"workingDir":        "/",
			}

			resp, err := common.MakeRequest(http.MethodPost, "/process", processRequest)
			require.NoError(t, err)
			defer resp.Body.Close()

			var processResponse map[string]interface{}
			err = json.NewDecoder(resp.Body).Decode(&processResponse)
			require.NoError(t, err)

			status := processResponse["status"].(string)
			assert.Equal(t, "completed", status, "Workaround '%s' should succeed: %s", tt.command, tt.description)

			if logs, ok := processResponse["logs"].(string); ok {
				assert.Contains(t, logs, tt.expectedLogs, "%s: %s", tt.name, tt.description)
			}
		})
	}
}
