package process

import (
	"os"
	"os/exec"
	"strings"
)

// maxArgStrlen is Linux MAX_ARG_STRLEN: execve rejects any single argv or env
// string this long with E2BIG ("argument list too long").
const maxArgStrlen = 32 * 4096

// largeScriptArg is passed in place of a command too long for argv: the shell
// sources the command from fd 3, leaving stdin to the "stdin" feature.
const largeScriptArg = ". /proc/self/fd/3"

// shellCommand runs command through $SHELL $SHELL_ARGS (default "sh -c"), so
// shell built-ins (cd, export, alias, exit) work. Commands that fit in argv are
// passed as-is; longer ones are fed through a pipe on fd 3. The returned func
// releases the parent's end of that pipe and must be called once the command
// has started or been abandoned.
func shellCommand(command string) (*exec.Cmd, func(), error) {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "sh"
	}
	shellArgs := os.Getenv("SHELL_ARGS")
	if shellArgs == "" {
		shellArgs = "-c"
	}
	args := strings.Fields(shellArgs)

	if len(command) < maxArgStrlen {
		return exec.Command(shell, append(args, command)...), func() {}, nil
	}

	r, w, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	// The shell reopens the pipe through /proc as the workload user, but pipes
	// are created 0600 for the API user.
	if err := r.Chmod(0o444); err != nil {
		r.Close()
		w.Close()
		return nil, nil, err
	}
	// Close fd 3 first so the processes the command starts don't inherit it.
	script := "exec 3<&-; " + command
	// Hold the whole script in the pipe so the shell can still read all of it
	// if sandbox-api restarts before it does. Best effort: the write below
	// blocks until the shell catches up otherwise.
	growPipe(w, len(script))
	go func() {
		// Fails with EPIPE if the shell exits or never starts; nothing to do.
		_, _ = w.WriteString(script)
		w.Close()
	}()

	cmd := exec.Command(shell, append(args, largeScriptArg)...)
	cmd.ExtraFiles = []*os.File{r}
	return cmd, func() { r.Close() }, nil
}
