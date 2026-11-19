package apierrors

import "net/http"

// The registered codes. Add an entry here, answer the emitting site with it
// (SendError in the handler package), add the site's row to catalog.csv, run
// the export test with UPDATE_EXPORT=1 and regenerate the error codes reference
// from registry.json (scripts/error-codes/generate.mjs in blaxel-ai/docs, on a
// branch named like this change's; reference_test.go fails until the page
// lists the code). Retry fields come from reading the
// emitting code path, and its owner confirms them in review. Every entry names
// its catalog.csv rows in Catalog. A status never changes here without an
// approved status code change.
var (
	// Request validation.

	// InvalidRequestBody: the JSON body does not parse or does not match the
	// endpoint's schema.
	InvalidRequestBody = Entry{
		Code:          "INVALID_REQUEST_BODY",
		Status:        http.StatusBadRequest,
		Message:       "invalid request body: {detail}",
		Action:        "Fix the request body to match the endpoint's schema in the API reference (/swagger/doc.json), then resend it.",
		DoNot:         "Do not resend the same body: it is refused the same way every time.",
		Retryable:     false,
		DispatchState: NotDispatched, // the body is parsed before the handler acts
		Catalog:       []string{"S0001", "S0002", "S0003"},
	}

	// InvalidParameter: a path or query parameter is missing or has a value the
	// endpoint does not accept.
	InvalidParameter = Entry{
		Code:          "INVALID_PARAMETER",
		Status:        http.StatusBadRequest,
		Message:       "{detail}",
		Action:        "Fix the parameter the message names (the API reference lists the accepted values), then resend the request.",
		Retryable:     false,
		DispatchState: NotDispatched, // parameters are checked before the handler acts
		Catalog:       []string{"S0004", "S0005", "S0006", "S0007", "S0008", "S0009", "S0010", "S0011", "S0012", "S0013", "S0014", "S0015"},
	}

	// InvalidPath: a path cannot be resolved: a relative path that leaves the
	// root directory, or ~ without a home directory.
	InvalidPath = Entry{
		Code:          "INVALID_PATH",
		Status:        http.StatusBadRequest,
		Message:       "{detail}",
		Action:        "Send an absolute path, or a relative path that stays inside the working directory, then resend the request.",
		Retryable:     false,
		DispatchState: NotDispatched, // the path is resolved before anything is read or written
		Catalog:       []string{"S0016", "S0017"},
	}

	// InvalidPathParameter: the tree route's path parameter is not a string.
	InvalidPathParameter = Entry{
		Code:          "INVALID_PATH_PARAMETER",
		Status:        http.StatusUnprocessableEntity,
		Message:       "invalid path parameter",
		Action:        "Send the path as part of the URL, as in /filesystem/tree/{path}, then resend the request.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked first, before any filesystem call
		Catalog:       []string{"S0018"},
	}

	// Filesystem.

	// FileNotFound: nothing exists at the path.
	FileNotFound = Entry{
		Code:          "FILE_NOT_FOUND",
		Status:        http.StatusNotFound,
		Message:       "file or directory not found",
		Action:        "Check the path: list its parent directory (GET /filesystem/{parent}) to see what exists, then resend with an existing path.",
		Retryable:     false,
		DispatchState: NotDispatched, // a lookup that found nothing; a delete answers this before deleting anything
		Catalog:       []string{"S0019", "S0020"},
	}

	// FileInfoFailed: the path exists but its information cannot be read.
	FileInfoFailed = Entry{
		Code:          "FILE_INFO_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "{detail}",
		Action:        "Check the permissions of the path and its parent directories, for example with ls -ld in a process.",
		Retryable:     false,
		DispatchState: NotDispatched, // a stat: it reads and changes nothing
		Catalog:       []string{"S0021"},
	}

	// PathIsDirectory: the endpoint needs a file and the path is a directory.
	PathIsDirectory = Entry{
		Code:          "PATH_IS_DIRECTORY",
		Status:        http.StatusBadRequest,
		Message:       "path is a directory, not a file",
		Action:        "Send the path of a file, or list the directory with GET /filesystem/{path}.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked before the file is read or edited
		Catalog:       []string{"S0022"},
	}

	// PathNotDirectory: the endpoint needs a directory and the path is not one
	// (or does not exist, for content search).
	PathNotDirectory = Entry{
		Code:          "PATH_NOT_DIRECTORY",
		Status:        http.StatusBadRequest,
		Message:       "path is not a directory",
		Action:        "Send the path of an existing directory, then resend the request.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked before the directory is read, watched or searched
		Catalog:       []string{"S0023", "S0024"},
	}

	// FilesystemReadFailed: a file or directory could not be read, opened,
	// listed or checked.
	FilesystemReadFailed = Entry{
		Code:          "FILESYSTEM_READ_FAILED",
		Status:        http.StatusUnprocessableEntity,
		Message:       "error reading file: {detail}",
		Action:        "Read the cause at the end of the message (often a permission or a file type the endpoint cannot read), fix the path or its permissions, then resend.",
		Retryable:     false,
		DispatchState: NotDispatched, // reads and existence checks change nothing; on writes and deletes they run before the change
		Catalog:       []string{"S0025", "S0026", "S0027", "S0028", "S0029", "S0030"},
	}

	// FilesystemWriteFailed: a file or directory could not be created, written,
	// copied or given its permissions.
	FilesystemWriteFailed = Entry{
		Code:          "FILESYSTEM_WRITE_FAILED",
		Status:        http.StatusUnprocessableEntity,
		Message:       "error writing file: {detail}",
		Action:        "Read the cause at the end of the message (often a permission or a missing parent), check what was written with GET /filesystem/{path}, fix the cause, then resend.",
		DoNot:         "Do not resend unchanged: it fails the same way, and part of the change may already be on disk.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the write may have started: a file can be partly written, a tree or a copy partly applied
		Catalog:       []string{"S0031", "S0032", "S0033", "S0034", "S0035"},
	}

	// FilesystemDeleteFailed: a file or directory could not be deleted, for
	// example a directory that is not empty without recursive=true.
	FilesystemDeleteFailed = Entry{
		Code:          "FILESYSTEM_DELETE_FAILED",
		Status:        http.StatusUnprocessableEntity,
		Message:       "error deleting directory: {detail}",
		Action:        "Read the cause at the end of the message; for a directory that is not empty, resend with recursive=true.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // a recursive delete can remove part of the tree before it fails
		Catalog:       []string{"S0036"},
	}

	// FileAlreadyExists: a copy with noOverwrite=true found an existing
	// destination, which it left unchanged.
	FileAlreadyExists = Entry{
		Code:          "FILE_ALREADY_EXISTS",
		Status:        http.StatusConflict,
		Message:       "error copying destination {path}: {detail}",
		Action:        "Copy to another destination, delete the existing one first, or resend without noOverwrite to replace it.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // a copy is not a transaction: entries copied before the existing one remain
		Catalog:       []string{"S0037"},
	}

	// DiskFull: the filesystem a write needed has no space left. Sent with the
	// status of the operation that hit it: 422 for most filesystem writes and
	// execs, 500 for multipart uploads.
	DiskFull = Entry{
		Code:          "DISK_FULL",
		Status:        http.StatusUnprocessableEntity,
		Message:       "{detail}. The sandbox's disk is full: no space is left on the filesystem that holds {path}{size}. Free space by deleting files you no longer need (DELETE /filesystem/{path}, or rm in a process), then resend the request. Don't resend it unchanged: it fails the same way until space is freed.",
		Action:        "Free space by deleting files you no longer need (DELETE /filesystem/{path}, or rm in a process), then resend the request.",
		DoNot:         "Do not resend it unchanged: it fails the same way until space is freed.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the write ran out of space part way
		Catalog:       []string{"S0038"},
	}

	// InvalidPermissions: permissions are not an octal mode.
	InvalidPermissions = Entry{
		Code:          "INVALID_PERMISSIONS",
		Status:        http.StatusBadRequest,
		Message:       "invalid permissions format '{permissions}': {detail}",
		Action:        "Send permissions as an octal string such as \"0644\", then resend the request.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // a multipart upload reads its parts in order, so the file can be written before a later permissions part is refused
		Catalog:       []string{"S0039", "S0040"},
	}

	// InvalidMultipartBody: the multipart body cannot be read.
	InvalidMultipartBody = Entry{
		Code:          "INVALID_MULTIPART_BODY",
		Status:        http.StatusBadRequest,
		Message:       "error reading multipart part: {detail}",
		Action:        "Send a multipart/form-data body with a file field, then resend the request.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the parts are read in order, so the file part can be written before a later part fails to read
		Catalog:       []string{"S0041"},
	}

	// MultipartFileMissing: the multipart body has no file field.
	MultipartFileMissing = Entry{
		Code:          "MULTIPART_FILE_MISSING",
		Status:        http.StatusBadRequest,
		Message:       "missing 'file' field in multipart form",
		Action:        "Add the content as a form field named \"file\" with a filename, then resend the request.",
		Retryable:     false,
		DispatchState: NotDispatched, // answered after reading every part, none of which was a file, so nothing was written
		Catalog:       []string{"S0042"},
	}

	// MultipartUnavailable: multipart uploads are not set up in this sandbox.
	MultipartUnavailable = Entry{
		Code:          "MULTIPART_UNAVAILABLE",
		Status:        http.StatusInternalServerError,
		Message:       "multipart upload not available",
		Action:        "Upload the file in one request with PUT /filesystem/{path} instead.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked first, before anything else
		Catalog:       []string{"S0043"},
	}

	// MultipartUploadNotFound: no multipart upload has this uploadId: it was
	// never initiated, or was completed or aborted.
	MultipartUploadNotFound = Entry{
		Code:          "MULTIPART_UPLOAD_NOT_FOUND",
		Status:        http.StatusNotFound,
		Message:       "upload not found: {upload_id}",
		Action:        "Initiate a new upload with POST /filesystem-multipart/initiate/{path} and use the uploadId it returns.",
		Retryable:     false,
		DispatchState: NotDispatched, // a lookup that found nothing
		Catalog:       []string{"S0044"},
	}

	// MultipartAbortFailed: the upload's parts could not all be removed. Sent
	// with 404, the status this endpoint always sent.
	MultipartAbortFailed = Entry{
		Code:          "MULTIPART_ABORT_FAILED",
		Status:        http.StatusNotFound,
		Message:       "failed to remove upload directory: {detail}",
		Action:        "Read the cause at the end of the message; the upload stays listed, so abort it again once the cause is fixed.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the upload's parts may be partly removed
		Catalog:       []string{"S0045"},
	}

	// MultipartUploadFailed: a multipart upload could not be initiated, a part
	// could not be stored, or the parts could not be assembled.
	MultipartUploadFailed = Entry{
		Code:          "MULTIPART_UPLOAD_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "failed to complete upload: {detail}",
		Action:        "Read the cause at the end of the message, then list the stored parts (GET /filesystem-multipart/{uploadId}/parts) and resend only what is missing.",
		DoNot:         "Do not resend blindly: parts may already be stored.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the upload directory, a part or the final file may be partly written
		Catalog:       []string{"S0046", "S0047", "S0048"},
	}

	// FilesystemWatchFailed: the directory watch could not start.
	FilesystemWatchFailed = Entry{
		Code:          "FILESYSTEM_WATCH_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "{detail}",
		Action:        "Read the cause in the message; on a large tree, watch a narrower directory or pass ignore patterns.",
		Retryable:     false,
		DispatchState: NotDispatched, // a watch only reads
		Catalog:       []string{"S0049"},
	}

	// FilesystemSearchFailed: a search could not walk the directory.
	FilesystemSearchFailed = Entry{
		Code:          "FILESYSTEM_SEARCH_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "error walking directory: {detail}",
		Action:        "Read the cause at the end of the message (often a directory the search may not read), then search a narrower directory.",
		Retryable:     false,
		DispatchState: NotDispatched, // a search only reads
		Catalog:       []string{"S0050"},
	}

	// Processes.

	// KeepAliveUnavailable: keepAlive was asked for on a sandbox whose plan does
	// not include it.
	KeepAliveUnavailable = Entry{
		Code:          "KEEP_ALIVE_UNAVAILABLE",
		Status:        http.StatusBadRequest,
		Message:       "keepAlive is not available for this sandbox: it is not included in your current plan",
		Action:        "Resend without keepAlive, or upgrade to a plan that includes it.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked before the process starts
		Catalog:       []string{"S0051"},
	}

	// ProcessCallbackInvalid: callbackUrl or callbackSecret is not usable.
	ProcessCallbackInvalid = Entry{
		Code:          "PROCESS_CALLBACK_INVALID",
		Status:        http.StatusBadRequest,
		Message:       "{detail}",
		Action:        "Send callbackUrl as an absolute http or https URL, with a callbackSecret of at least the length the message names, then resend.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked before the process starts
		Catalog:       []string{"S0052"},
	}

	// ProcessNameInUse: a running process already has this name.
	ProcessNameInUse = Entry{
		Code:          "PROCESS_NAME_IN_USE",
		Status:        http.StatusBadRequest,
		Message:       "process with name '{name}' already exists and is running",
		Action:        "Use another name, or stop the running process (DELETE /process/{name}) or wait for it (GET /process/{name}/wait) before reusing its name.",
		DoNot:         "Do not resend unchanged while the process runs: the name stays taken.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked before the process starts
		Catalog:       []string{"S0053"},
	}

	// ProcessWaitLimitReached: the exec waited the longest one request may (115
	// seconds, under the edge gateway's 120) and the process is still running,
	// or its ports are not all open yet. It keeps running.
	ProcessWaitLimitReached = Entry{
		Code:          "PROCESS_WAIT_LIMIT_REACHED",
		Status:        http.StatusUnprocessableEntity,
		Message:       "process \"{name}\" (PID {pid}) {state} after {seconds} seconds, the longest one exec request waits: the edge gateway closes requests after {edge_seconds} seconds. It keeps running {until}. {next} Don't resend the exec: that starts a second copy.",
		Action:        "Wait for the process with GET /process/{identifier}/wait, or follow its output with GET /process/{identifier}/logs/stream. To wait longer in one call, stream the exec (Accept: application/x-ndjson).",
		DoNot:         "Do not resend the exec: that starts a second copy of the command.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the process started and is still running
		Catalog:       []string{"S0054"},
	}

	// ProcessWorkingDirUnusable: workingDir exists and is not a directory, a
	// parent is a file, or the command's user may not create it. A workingDir
	// that is only missing is created.
	ProcessWorkingDirUnusable = Entry{
		Code:          "PROCESS_WORKING_DIR_UNUSABLE",
		Status:        http.StatusUnprocessableEntity,
		Message:       "{detail}. The command did not run, and resending it unchanged fails the same way. {next}",
		Action:        "Set workingDir to a directory the command's user can use (or create), then resend the exec.",
		DoNot:         "Do not resend unchanged: it fails the same way until the path changes.",
		Retryable:     false,
		DispatchState: NotDispatched, // the working directory is checked before the process starts
		Catalog:       []string{"S0055"},
	}

	// ProcessStartFailed: the process could not be started: its shell, log files
	// or stdin could not be set up, or the start itself failed.
	ProcessStartFailed = Entry{
		Code:          "PROCESS_START_FAILED",
		Status:        http.StatusUnprocessableEntity,
		Message:       "failed to start process: {detail}",
		Action:        "Read the cause at the end of the message (often a missing shell or binary, or a full disk), fix it, then resend the exec.",
		Retryable:     false,
		DispatchState: NotDispatched, // every failure this code covers returns before the command is started
		Catalog:       []string{"S0056"},
	}

	// ProcessTimedOut: the exec waited for completion and the process did not
	// exit within the wait.
	ProcessTimedOut = Entry{
		Code:          "PROCESS_TIMED_OUT",
		Status:        http.StatusUnprocessableEntity,
		Message:       "process timed out after {seconds} seconds",
		Action:        "Read the process's state and output with GET /process/{identifier}, then raise timeout or run it without waitForCompletion and follow it with GET /process/{identifier}/wait.",
		DoNot:         "Do not resend unchanged: the command already ran and may have changed the sandbox.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the command ran until its wait ended
		Catalog:       []string{"S0057"},
	}

	// ProcessPortsTimeout: the process did not open every port in waitForPorts
	// within the wait.
	ProcessPortsTimeout = Entry{
		Code:          "PROCESS_PORTS_TIMEOUT",
		Status:        http.StatusUnprocessableEntity,
		Message:       "process timed out waiting for ports after {seconds} seconds",
		Action:        "Follow the process's output with GET /process/{identifier}/logs/stream to see why its ports are not open, and check that it listens on 0.0.0.0, not only 127.0.0.1.",
		DoNot:         "Do not resend unchanged: the command already started and may still be running.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the command was started and ran while the ports were awaited
		Catalog:       []string{"S0058"},
	}

	// ProcessExitedBeforePorts: the process failed before it opened the ports in
	// waitForPorts; the message ends with its last output.
	ProcessExitedBeforePorts = Entry{
		Code:          "PROCESS_EXITED_BEFORE_PORTS",
		Status:        http.StatusUnprocessableEntity,
		Message:       "process exited with code {exit_code} before ports {ports} opened; last output:\n{output}",
		Action:        "Read the last output at the end of the message to see why the process failed, fix the command, then resend the exec.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the command ran and failed
		Catalog:       []string{"S0059"},
	}

	// ProcessExecFailed: the process started but its record could not be found
	// when the exec answered.
	ProcessExecFailed = Entry{
		Code:          "PROCESS_EXEC_FAILED",
		Status:        http.StatusUnprocessableEntity,
		Message:       "process creation failed because process does not exist",
		Action:        "List processes with GET /process to see whether the command is running before you resend it.",
		DoNot:         "Do not resend without checking: the command may be running.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the process had started when its record went missing
		Catalog:       []string{"S0060"},
	}

	// ProcessStreamFailed: a streamed exec started the process, then could not
	// stream its output or read its final state. Only sent as a stream error
	// event, whose status field says 500.
	ProcessStreamFailed = Entry{
		Code:          "PROCESS_STREAM_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "{detail}",
		Action:        "Read the process's state with GET /process/{identifier} and its output with GET /process/{identifier}/logs.",
		DoNot:         "Do not resend the exec: the process already started.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the process had started when the stream failed
		Catalog:       []string{"S0061"},
	}

	// ProcessNotFound: no process has this PID or name.
	ProcessNotFound = Entry{
		Code:          "PROCESS_NOT_FOUND",
		Status:        http.StatusNotFound,
		Message:       "process with Identifier {identifier} not found",
		Action:        "List processes with GET /process to find the right PID or name.",
		Retryable:     false,
		DispatchState: NotDispatched, // a lookup that found nothing
		Catalog:       []string{"S0062"},
	}

	// ProcessNotRunning: the process exists but is not running, so there is
	// nothing to stop or kill. Sent with 404, the status these endpoints always
	// sent.
	ProcessNotRunning = Entry{
		Code:          "PROCESS_NOT_RUNNING",
		Status:        http.StatusNotFound,
		Message:       "process with Identifier {identifier} is not running",
		Action:        "Read the process's final state with GET /process/{identifier}: it already ended.",
		Retryable:     false,
		DispatchState: NotDispatched, // refused before any signal is sent
		Catalog:       []string{"S0063"},
	}

	// ProcessSignalFailed: the stop or kill signal could not be sent. Sent with
	// 404, the status these endpoints always sent.
	ProcessSignalFailed = Entry{
		Code:          "PROCESS_SIGNAL_FAILED",
		Status:        http.StatusNotFound,
		Message:       "failed to send {signal} to process {identifier}: {detail}",
		Action:        "Read the cause at the end of the message, then check the process with GET /process/{identifier}.",
		Retryable:     false,
		DispatchState: NotDispatched, // the signal was refused by the kernel, so it was not delivered
		Catalog:       []string{"S0064"},
	}

	// ProcessLogFormatUnsupported: NDJSON logs were asked for a process whose
	// logs were recorded before that format existed.
	ProcessLogFormatUnsupported = Entry{
		Code:          "PROCESS_LOG_FORMAT_UNSUPPORTED",
		Status:        http.StatusConflict,
		Message:       "structured logs are unavailable for this process; start a new process",
		Action:        "Stream the logs as text (without Accept: application/x-ndjson), or start a new process.",
		Retryable:     false,
		DispatchState: NotDispatched, // refused before the stream starts
		Catalog:       []string{"S0065"},
	}

	// ProcessLogStreamFailed: the log stream could not start or failed part way.
	// On an NDJSON stream it comes as an error event.
	ProcessLogStreamFailed = Entry{
		Code:          "PROCESS_LOG_STREAM_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "{detail}",
		Action:        "Read the logs in one call with GET /process/{identifier}/logs.",
		Retryable:     false,
		DispatchState: NotDispatched, // reading logs changes nothing
		Catalog:       []string{"S0066"},
	}

	// ProcessStdinTooLarge: the stdin body is over 8 MiB.
	ProcessStdinTooLarge = Entry{
		Code:          "PROCESS_STDIN_TOO_LARGE",
		Status:        http.StatusRequestEntityTooLarge,
		Message:       "http: request body too large",
		Action:        "Split the input into several requests of at most 8 MiB; they are written in order.",
		Retryable:     false,
		DispatchState: NotDispatched, // the body is read in full before anything is written to stdin
		Catalog:       []string{"S0067"},
	}

	// ProcessStdinNotEnabled: the process was started without stdin.
	ProcessStdinNotEnabled = Entry{
		Code:          "PROCESS_STDIN_NOT_ENABLED",
		Status:        http.StatusConflict,
		Message:       "process was started without stdin; start it with \"stdin\": true",
		Action:        "Start the process again with \"stdin\": true.",
		Retryable:     false,
		DispatchState: NotDispatched, // refused before anything is written
		Catalog:       []string{"S0068"},
	}

	// ProcessStdinClosed: stdin was closed, the process exited, or the sandbox
	// API restarted (the pipe does not survive it).
	ProcessStdinClosed = Entry{
		Code:          "PROCESS_STDIN_CLOSED",
		Status:        http.StatusConflict,
		Message:       "stdin is closed; restart the process to get a new one",
		Action:        "Start the process again to get a new stdin.",
		Retryable:     false,
		DispatchState: NotDispatched, // refused before anything is written
		Catalog:       []string{"S0069"},
	}

	// ProcessStdinStalled: the process stopped reading its stdin and the pipe
	// stayed full.
	ProcessStdinStalled = Entry{
		Code:          "PROCESS_STDIN_STALLED",
		Status:        http.StatusServiceUnavailable,
		Message:       "process is not reading its stdin",
		Action:        "Check that the process still reads its input (GET /process/{identifier}/logs); part of the body may have been written, so decide what to send next from its output.",
		DoNot:         "Do not resend the same body: part of it may already be written, and it would arrive twice.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // part of the body may have been written before the pipe stayed full
		Catalog:       []string{"S0070"},
	}

	// ProcessStdinFailed: writing to or closing stdin failed.
	ProcessStdinFailed = Entry{
		Code:          "PROCESS_STDIN_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "{detail}",
		Action:        "Read the cause in the message and check the process with GET /process/{identifier} before sending more input.",
		DoNot:         "Do not resend the same body: part of it may already be written.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // a write may have been partly done
		Catalog:       []string{"S0071"},
	}

	// Network.

	// PortsReadFailed: the open ports of the process cannot be read.
	PortsReadFailed = Entry{
		Code:          "PORTS_READ_FAILED",
		Status:        http.StatusUnprocessableEntity,
		Message:       "{detail}",
		Action:        "Check that the PID belongs to a running process (GET /process), then resend.",
		Retryable:     false,
		DispatchState: NotDispatched, // reading ports changes nothing
		Catalog:       []string{"S0072"},
	}

	// TunnelConfigInvalid: the tunnel config does not decode.
	TunnelConfigInvalid = Entry{
		Code:          "TUNNEL_CONFIG_INVALID",
		Status:        http.StatusUnprocessableEntity,
		Message:       "invalid tunnel config: {detail}",
		Action:        "Send the tunnel config base64-encoded, as the API reference describes, then resend.",
		Retryable:     false,
		DispatchState: NotDispatched, // the config is parsed before it is applied
		Catalog:       []string{"S0073"},
	}

	// TunnelConfigFailed: the tunnel config could not be applied.
	TunnelConfigFailed = Entry{
		Code:          "TUNNEL_CONFIG_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "failed to apply tunnel config: {detail}",
		Action:        "Read the cause at the end of the message, then apply the config again once it is fixed.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // applying the config can stop the previous tunnel before it fails
		Catalog:       []string{"S0074"},
	}

	// TunnelNotRunning: no tunnel is running, so there is nothing to disconnect.
	TunnelNotRunning = Entry{
		Code:          "TUNNEL_NOT_RUNNING",
		Status:        http.StatusBadRequest,
		Message:       "no WireGuard client is running",
		Action:        "Nothing to do: the tunnel is already disconnected.",
		Retryable:     false,
		DispatchState: NotDispatched, // nothing to stop
		Catalog:       []string{"S0075"},
	}

	// TunnelStopFailed: the tunnel could not be stopped. Sent with 400, the
	// status this endpoint always sent.
	TunnelStopFailed = Entry{
		Code:          "TUNNEL_STOP_FAILED",
		Status:        http.StatusBadRequest,
		Message:       "failed to stop WireGuard client: {detail}",
		Action:        "Read the cause at the end of the message, then disconnect again.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the stop may have started
		Catalog:       []string{"S0076"},
	}

	// Archive.

	// ArchiveOptionsInvalid: the export options are incomplete, or name an image
	// the archive cannot be compared against.
	ArchiveOptionsInvalid = Entry{
		Code:          "ARCHIVE_OPTIONS_INVALID",
		Status:        http.StatusBadRequest,
		Message:       "{detail}",
		Action:        "Fix the option the message names, then resend the export.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked before the sandbox is frozen
		Catalog:       []string{"S0077"},
	}

	// SandboxAlreadyFrozen: the sandbox is already frozen by an earlier export.
	SandboxAlreadyFrozen = Entry{
		Code:          "SANDBOX_ALREADY_FROZEN",
		Status:        http.StatusConflict,
		Message:       "sandbox is already frozen",
		Action:        "Follow the earlier export on GET /archive/status, or lift the freeze with POST /archive/resume before exporting again.",
		Retryable:     false,
		DispatchState: NotDispatched, // refused before a second freeze
		Catalog:       []string{"S0078"},
	}

	// ArchiveInProgress: another export (or dry run) or a restore is running.
	ArchiveInProgress = Entry{
		Code:          "ARCHIVE_IN_PROGRESS",
		Status:        http.StatusConflict,
		Message:       "an archive export is in progress",
		Action:        "Wait for the running operation to end (GET /archive/status), then resend.",
		Retryable:     true,
		DispatchState: NotDispatched, // refused before anything is done; the other operation keeps running
		Catalog:       []string{"S0079"},
	}

	// ArchiveExportFailed: the export failed.
	ArchiveExportFailed = Entry{
		Code:          "ARCHIVE_EXPORT_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "{detail}",
		Action:        "Read GET /archive/status for the sandbox's state; lift a freeze left behind with POST /archive/resume before exporting again.",
		DoNot:         "Do not resend the export without checking the status first.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the export may have frozen the sandbox, stopped processes or uploaded part of the archive
		Catalog:       []string{"S0080"},
	}

	// ArchiveResumeFailed: the freeze was lifted but the root filesystem stayed
	// read-only, so the sandbox is not usable.
	ArchiveResumeFailed = Entry{
		Code:          "ARCHIVE_RESUME_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "{detail}",
		Action:        "Recreate the sandbox: its filesystem stays read-only.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the freeze was lifted on the API before the filesystem failed to become writable
		Catalog:       []string{"S0081"},
	}

	// SandboxFrozen: the sandbox is frozen for an archive, so endpoints that
	// change it are refused. This body keeps its status field as the archive
	// status object it always was.
	SandboxFrozen = Entry{
		Code:          "SANDBOX_FROZEN",
		Status:        http.StatusServiceUnavailable,
		Message:       "sandbox is {state} ({reason}): this endpoint is unavailable until the archive completes, follow it on /archive/status",
		Action:        "Follow the archive on GET /archive/status; once it ends (or after POST /archive/resume), resend the request.",
		Retryable:     false,
		DispatchState: NotDispatched, // refused by middleware before any handler
		Catalog:       []string{"S0082"},
	}

	// Drives.

	// DriveRequestInvalid: a drive name, mount path or drive path is missing or
	// not allowed.
	DriveRequestInvalid = Entry{
		Code:          "DRIVE_REQUEST_INVALID",
		Status:        http.StatusBadRequest,
		Message:       "{detail}",
		Action:        "Fix the field the message names (mount and drive paths start with /), then resend.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked before anything is mounted or unmounted
		Catalog:       []string{"S0083", "S0084", "S0085"},
	}

	// DriveMountPathSymlink: the mount path is a symbolic link.
	DriveMountPathSymlink = Entry{
		Code:          "DRIVE_MOUNT_PATH_SYMLINK",
		Status:        http.StatusBadRequest,
		Message:       "Failed to mount drive: {detail}",
		Action:        "Mount on a path that is not a symbolic link.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked before mounting
		Catalog:       []string{"S0086"},
	}

	// DriveMountPathBusy: something is already mounted at the mount path.
	DriveMountPathBusy = Entry{
		Code:          "DRIVE_MOUNT_PATH_BUSY",
		Status:        http.StatusConflict,
		Message:       "Failed to mount drive: {detail}",
		Action:        "Unmount what is there (DELETE /drives/mount/{path}) or choose another mount path.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked before mounting
		Catalog:       []string{"S0087"},
	}

	// DriveAccessDenied: the drive refused access.
	DriveAccessDenied = Entry{
		Code:          "DRIVE_ACCESS_DENIED",
		Status:        http.StatusForbidden,
		Message:       "Failed to mount drive: {detail}",
		Action:        "Check that the drive exists in this workspace and that the sandbox may use it.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the mount was attempted
		Catalog:       []string{"S0088"},
	}

	// DriveInsufficientStorage: the sandbox has too little disk or memory to
	// mount the drive.
	DriveInsufficientStorage = Entry{
		Code:          "DRIVE_INSUFFICIENT_STORAGE",
		Status:        http.StatusInsufficientStorage,
		Message:       "Failed to mount drive: {detail}",
		Action:        "Free disk space or memory in the sandbox, or use a sandbox with more, then mount again.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the mount was attempted
		Catalog:       []string{"S0089"},
	}

	// DriveMountFailed: the drive could not be mounted.
	DriveMountFailed = Entry{
		Code:          "DRIVE_MOUNT_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "Failed to mount drive: {detail}",
		Action:        "Read the cause in the message and list mounts with GET /drives/mount before mounting again.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the mount was attempted
		Catalog:       []string{"S0090"},
	}

	// DriveNotMounted: no drive is mounted at the path.
	DriveNotMounted = Entry{
		Code:          "DRIVE_NOT_MOUNTED",
		Status:        http.StatusNotFound,
		Message:       "Failed to unmount drive: no drive mounted at this path",
		Action:        "List mounts with GET /drives/mount to find the right path.",
		Retryable:     false,
		DispatchState: NotDispatched, // nothing to unmount
		Catalog:       []string{"S0091"},
	}

	// DriveUnmountFailed: the drive could not be unmounted, for example because
	// a process still uses it.
	DriveUnmountFailed = Entry{
		Code:          "DRIVE_UNMOUNT_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "Failed to unmount drive: {detail}",
		Action:        "Stop the processes that use the mount path, then unmount again.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the unmount was attempted
		Catalog:       []string{"S0092"},
	}

	// DriveListFailed: the mounted drives cannot be listed.
	DriveListFailed = Entry{
		Code:          "DRIVE_LIST_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "Failed to list mounts: {detail}",
		Action:        "Read the cause at the end of the message.",
		Retryable:     false,
		DispatchState: NotDispatched, // listing changes nothing
		Catalog:       []string{"S0093"},
	}

	// Codegen.

	// CodegenNotConfigured: codegen tools have no provider configured in this
	// sandbox.
	CodegenNotConfigured = Entry{
		Code:          "CODEGEN_NOT_CONFIGURED",
		Status:        http.StatusBadRequest,
		Message:       "codegen tools are not configured, follow this documentation to configure it: https://docs.blaxel.ai/Sandboxes/Codegen",
		Action:        "Configure a codegen provider as https://docs.blaxel.ai/Sandboxes/Codegen describes, then resend.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked first
		Catalog:       []string{"S0094"},
	}

	// CodegenClientFailed: the codegen provider client cannot be created,
	// usually a missing or invalid provider setting.
	CodegenClientFailed = Entry{
		Code:          "CODEGEN_CLIENT_FAILED",
		Status:        http.StatusUnprocessableEntity,
		Message:       "{detail}",
		Action:        "Fix the codegen provider settings the message names, then resend.",
		Retryable:     false,
		DispatchState: NotDispatched, // fails before the provider is called or the file is written
		Catalog:       []string{"S0095"},
	}

	// CodegenApplyFailed: the codegen provider could not apply the edit. The
	// file is unchanged.
	CodegenApplyFailed = Entry{
		Code:          "CODEGEN_APPLY_FAILED",
		Status:        http.StatusUnprocessableEntity,
		Message:       "{detail}",
		Action:        "Read the provider's error in the message; fix the edit or the provider settings, then resend.",
		Retryable:     false,
		DispatchState: NotDispatched, // the provider failed before the file was written
		Catalog:       []string{"S0096"},
	}

	// CodegenRerankingUnavailable: code reranking is no longer offered by the
	// providers.
	CodegenRerankingUnavailable = Entry{
		Code:          "CODEGEN_RERANKING_UNAVAILABLE",
		Status:        http.StatusServiceUnavailable,
		Message:       "code reranking is no longer available: Relace and Morph have both discontinued their reranking APIs",
		Action:        "Search the code with GET /filesystem-content-search/{path} instead.",
		Retryable:     false,
		DispatchState: NotDispatched, // refused before any work
		Catalog:       []string{"S0097"},
	}

	// CodegenRerankFailed: the provider could not rerank the files.
	CodegenRerankFailed = Entry{
		Code:          "CODEGEN_RERANK_FAILED",
		Status:        http.StatusUnprocessableEntity,
		Message:       "{detail}",
		Action:        "Search the code with GET /filesystem-content-search/{path} instead.",
		Retryable:     false,
		DispatchState: NotDispatched, // reranking only reads
		Catalog:       []string{"S0098"},
	}

	// Terminal.

	// TerminalStartFailed: the terminal session could not be created.
	TerminalStartFailed = Entry{
		Code:          "TERMINAL_START_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "{detail}",
		Action:        "Read the cause in the message (often the shell or working directory), then open the terminal again.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the shell may have been started
		Catalog:       []string{"S0099"},
	}

	// TerminalSessionNotFound: no terminal session has this id.
	TerminalSessionNotFound = Entry{
		Code:          "TERMINAL_SESSION_NOT_FOUND",
		Status:        http.StatusNotFound,
		Message:       "terminal session \"{session_id}\" not found: open it with GET /terminal/stream first",
		Action:        "Open the session with GET /terminal/stream first, then send input to it.",
		Retryable:     false,
		DispatchState: NotDispatched, // a lookup that found nothing
		Catalog:       []string{"S0100"},
	}

	// TerminalInputTooLarge: the terminal input is over the size limit.
	TerminalInputTooLarge = Entry{
		Code:          "TERMINAL_INPUT_TOO_LARGE",
		Status:        http.StatusRequestEntityTooLarge,
		Message:       "input is larger than {bytes} bytes",
		Action:        "Send the input in smaller messages.",
		Retryable:     false,
		DispatchState: NotDispatched, // refused before anything is written
		Catalog:       []string{"S0101"},
	}

	// TerminalMessageInvalid: the terminal message cannot be read or is not an
	// input or resize message.
	TerminalMessageInvalid = Entry{
		Code:          "TERMINAL_MESSAGE_INVALID",
		Status:        http.StatusBadRequest,
		Message:       "invalid terminal message: {detail}",
		Action:        "Send {\"type\":\"input\",\"data\":\"...\"} or {\"type\":\"resize\",\"cols\":N,\"rows\":N}.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked before anything is written
		Catalog:       []string{"S0102"},
	}

	// TerminalSessionClosed: the terminal session no longer accepts input.
	TerminalSessionClosed = Entry{
		Code:          "TERMINAL_SESSION_CLOSED",
		Status:        http.StatusConflict,
		Message:       "terminal session \"{session_id}\" is not accepting input: {detail}",
		Action:        "Open a new session with GET /terminal/stream.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // the write may have been partly done
		Catalog:       []string{"S0103"},
	}

	// TerminalResizeFailed: the terminal session could not be resized.
	TerminalResizeFailed = Entry{
		Code:          "TERMINAL_RESIZE_FAILED",
		Status:        http.StatusConflict,
		Message:       "failed to resize terminal session \"{session_id}\": {detail}",
		Action:        "Open a new session with GET /terminal/stream if this one has ended.",
		Retryable:     false,
		DispatchState: NotDispatched, // a failed resize leaves the size unchanged
		Catalog:       []string{"S0104"},
	}

	// Environment and system.

	// EnvironmentFileNotFound: the sandbox metadata file the environment is
	// reloaded from does not exist.
	EnvironmentFileNotFound = Entry{
		Code:          "ENVIRONMENT_FILE_NOT_FOUND",
		Status:        http.StatusNotFound,
		Message:       "{detail}",
		Action:        "Nothing to reload in this sandbox: set environment variables per process with env instead.",
		Retryable:     false,
		DispatchState: NotDispatched, // read before anything is applied
		Catalog:       []string{"S0105"},
	}

	// EnvironmentReloadFailed: the environment could not be reloaded.
	EnvironmentReloadFailed = Entry{
		Code:          "ENVIRONMENT_RELOAD_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "{detail}",
		Action:        "Read the cause in the message.",
		Retryable:     false,
		DispatchState: DispatchedUnknown, // variables may be partly applied
		Catalog:       []string{"S0106"},
	}

	// UpgradeVersionInvalid: the version is not latest, develop, main or a
	// release tag, or a custom baseUrl was sent.
	UpgradeVersionInvalid = Entry{
		Code:          "UPGRADE_VERSION_INVALID",
		Status:        http.StatusBadRequest,
		Message:       "invalid upgrade version or custom baseUrl",
		Action:        "Send \"latest\", \"develop\", \"main\" or a release tag such as \"v1.0.0\", without baseUrl.",
		Retryable:     false,
		DispatchState: NotDispatched, // checked before state is saved or the upgrade starts
		Catalog:       []string{"S0107"},
	}

	// UpgradeStateSaveFailed: the process state could not be saved, so the
	// upgrade did not start.
	UpgradeStateSaveFailed = Entry{
		Code:          "UPGRADE_STATE_SAVE_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "{detail}",
		Action:        "Read the cause in the message (often a full disk), fix it, then upgrade again.",
		Retryable:     false,
		DispatchState: NotDispatched, // the upgrade starts only after the state is saved
		Catalog:       []string{"S0108"},
	}

	// StandbyUnsupported: standby is not available in this environment.
	StandbyUnsupported = Entry{
		Code:          "STANDBY_UNSUPPORTED",
		Status:        http.StatusNotImplemented,
		Message:       "standby is not supported in this environment",
		Action:        "Let the sandbox go to standby on its own once it is idle.",
		Retryable:     false,
		DispatchState: NotDispatched, // refused before the standby is requested
		Catalog:       []string{"S0109"},
	}

	// SandboxHeldAwake: a keepAlive process or an archive operation holds the
	// sandbox awake.
	SandboxHeldAwake = Entry{
		Code:          "SANDBOX_HELD_AWAKE",
		Status:        http.StatusConflict,
		Message:       "the sandbox is held awake by a keepAlive process or an archive operation",
		Action:        "Stop the keepAlive processes, or resend with force=true to release their holds for the standby.",
		Retryable:     false,
		DispatchState: NotDispatched, // refused before the standby is requested
		Catalog:       []string{"S0110"},
	}

	// StandbyStateUnknown: the keep-awake state could not be read.
	StandbyStateUnknown = Entry{
		Code:          "STANDBY_STATE_UNKNOWN",
		Status:        http.StatusServiceUnavailable,
		Message:       "could not read the scale-to-zero counter",
		Action:        "Resend the request in a few seconds.",
		Retryable:     true,
		DispatchState: NotDispatched, // refused before the standby is requested
		Catalog:       []string{"S0111"},
	}

	// StandbyFailed: the standby could not be checked.
	StandbyFailed = Entry{
		Code:          "STANDBY_FAILED",
		Status:        http.StatusInternalServerError,
		Message:       "{detail}",
		Action:        "Read the cause in the message; the sandbox still goes to standby on its own once idle.",
		Retryable:     false,
		DispatchState: NotDispatched, // refused before the standby is requested
		Catalog:       []string{"S0112"},
	}
)

var entries = []Entry{
	InvalidRequestBody,
	InvalidParameter,
	InvalidPath,
	InvalidPathParameter,
	FileNotFound,
	FileInfoFailed,
	PathIsDirectory,
	PathNotDirectory,
	FilesystemReadFailed,
	FilesystemWriteFailed,
	FilesystemDeleteFailed,
	FileAlreadyExists,
	DiskFull,
	InvalidPermissions,
	InvalidMultipartBody,
	MultipartFileMissing,
	MultipartUnavailable,
	MultipartUploadNotFound,
	MultipartAbortFailed,
	MultipartUploadFailed,
	FilesystemWatchFailed,
	FilesystemSearchFailed,
	KeepAliveUnavailable,
	ProcessCallbackInvalid,
	ProcessNameInUse,
	ProcessWaitLimitReached,
	ProcessWorkingDirUnusable,
	ProcessStartFailed,
	ProcessTimedOut,
	ProcessPortsTimeout,
	ProcessExitedBeforePorts,
	ProcessExecFailed,
	ProcessStreamFailed,
	ProcessNotFound,
	ProcessNotRunning,
	ProcessSignalFailed,
	ProcessLogFormatUnsupported,
	ProcessLogStreamFailed,
	ProcessStdinTooLarge,
	ProcessStdinNotEnabled,
	ProcessStdinClosed,
	ProcessStdinStalled,
	ProcessStdinFailed,
	PortsReadFailed,
	TunnelConfigInvalid,
	TunnelConfigFailed,
	TunnelNotRunning,
	TunnelStopFailed,
	ArchiveOptionsInvalid,
	SandboxAlreadyFrozen,
	ArchiveInProgress,
	ArchiveExportFailed,
	ArchiveResumeFailed,
	SandboxFrozen,
	DriveRequestInvalid,
	DriveMountPathSymlink,
	DriveMountPathBusy,
	DriveAccessDenied,
	DriveInsufficientStorage,
	DriveMountFailed,
	DriveNotMounted,
	DriveUnmountFailed,
	DriveListFailed,
	CodegenNotConfigured,
	CodegenClientFailed,
	CodegenApplyFailed,
	CodegenRerankingUnavailable,
	CodegenRerankFailed,
	TerminalStartFailed,
	TerminalSessionNotFound,
	TerminalInputTooLarge,
	TerminalMessageInvalid,
	TerminalSessionClosed,
	TerminalResizeFailed,
	EnvironmentFileNotFound,
	EnvironmentReloadFailed,
	UpgradeVersionInvalid,
	UpgradeStateSaveFailed,
	StandbyUnsupported,
	SandboxHeldAwake,
	StandbyStateUnknown,
	StandbyFailed,
}
