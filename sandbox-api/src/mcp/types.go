package mcp

import (
	"encoding/json"
	"fmt"

	mcp_golang "github.com/metoro-io/mcp-golang"
)

// ProcessExecuteArgs represents arguments for process-related tools
type ProcessExecuteArgs struct {
	Command           string            `json:"command" jsonschema:"required,description=The command to execute"`
	Name              string            `json:"name" jsonschema:"description=Technical name for the process,default="`
	WorkingDir        string            `json:"workingDir" jsonschema:"description=The working directory for the command,default=/"`
	Env               map[string]string `json:"env" jsonschema:"description=Environment variables to set for the command,default={}"`
	WaitForCompletion bool              `json:"waitForCompletion" jsonschema:"description=Whether to wait for the command to complete before returning"`
	Timeout           int               `json:"timeout" jsonschema:"description=Timeout in seconds for the command,default=30"`
	WaitForPorts      []int             `json:"waitForPorts" jsonschema:"description=List of ports to wait for before returning"`
	IncludeLogs       bool              `json:"includeLogs" jsonschema:"description=Whether to include logs in the response"`
	RestartOnFailure  bool              `json:"restartOnFailure" jsonschema:"description=Whether to restart the process automatically on failure,default=false"`
	MaxRestarts       int               `json:"maxRestarts" jsonschema:"description=Maximum number of restart attempts (0 = no limit),default=0"`
}

// ProcessIdentifierArgs represents arguments for process identifier-related tools
type ProcessIdentifierArgs struct {
	Identifier string `json:"identifier" jsonschema:"required,description=Process identifier (PID or name)"`
}

type FsListDirectoryArgs struct {
	Path string `json:"path" jsonschema:"required,description=Path to the file or directory"`
}

type FsReadFileArgs struct {
	Path string `json:"path" jsonschema:"required,description=Path to the file"`
}

type FsWriteArgs struct {
	Path        string `json:"path" jsonschema:"required,description=Path to the file or directory"`
	Content     string `json:"content" jsonschema:"description=Content to write to the file"`
	Permissions string `json:"permissions" jsonschema:"description=Permissions for the file or directory (octal string)"`
	IsDirectory bool   `json:"isDirectory" jsonschema:"description=Whether the path refers to a directory"`
}

type FsDeleteArgs struct {
	Path        string `json:"path" jsonschema:"required,description=Path to the file or directory"`
	IsDirectory bool   `json:"isDirectory" jsonschema:"description=Whether the path refers to a directory"`
	Recursive   bool   `json:"recursive" jsonschema:"description=Whether to perform the operation recursively"`
}

// NetworkArgs represents arguments for network-related tools
type NetworkArgs struct {
	PID      int    `json:"pid" jsonschema:"required,description=Process ID"`
	Callback string `json:"callback" jsonschema:"description=Callback URL for port monitoring notifications"`
}

// Helper function to create JSON response
func CreateJSONResponse(data interface{}) (*mcp_golang.ToolResponse, error) {
	jsonBytes, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}
	return mcp_golang.NewToolResponse(mcp_golang.NewTextContent(string(jsonBytes))), nil
}
