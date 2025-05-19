package constants

type ProcessStatus string

// Process status constants
const (
	ProcessStatusFailed    ProcessStatus = "failed"
	ProcessStatusKilled    ProcessStatus = "killed"
	ProcessStatusStopped   ProcessStatus = "stopped"
	ProcessStatusRunning   ProcessStatus = "running"
	ProcessStatusCompleted ProcessStatus = "completed"
)
