package internal

import "os"

// Determines if the current process is running in a pipeline (stdin is coming
// from a pipe). Returns (true, nil) if stdin is piped/redirected, (false, nil)
// if attached to a TTY/char device. Returns an error if the stat call fails.
func IsPipe() (bool, error) {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false, err
	}

	return (fi.Mode() & os.ModeCharDevice) == 0, nil
}

// IsTerminal reports whether stdout is a terminal (character device). Returns
// (true, nil) when stdout is a terminal, (false, nil) when redirected/piped.
// Returns an error if the stat call fails.
func IsTerminal() (bool, error) {
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false, err
	}
	return (fi.Mode() & os.ModeCharDevice) != 0, nil
}

type EditorRunner func(path string) error

func DefaultEditor(path string) error {
	// Determine editor: prefer VISUAL, then EDITOR, then fallback to "nano".
	editor := os.Getenv("VISUAL")
	if editor == "" {
		editor = os.Getenv("EDITOR")
	}
	if editor == "" {
		editor = "nano"
	}

	// Prefer running via a shell so we support complex editor strings like
	// "code --wait". Use COMSPEC on Windows if present, otherwise /bin/sh.
	shell := os.Getenv("COMSPEC")
	shellFlag := "/C"
	if shell == "" {
		shell = "/bin/sh"
		shellFlag = "-c"
	}

	// Build the shell invocation: shell -c "<editor> <path>"
	// We keep this simple and avoid adding extra imports by using os.StartProcess.
	cmdLine := editor + " " + path
	args := []string{shell, shellFlag, cmdLine}

	attr := &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}

	proc, err := os.StartProcess(shell, args, attr)
	if err != nil {
		return err
	}
	_, err = proc.Wait()
	return err
}

func Edit(path string) {
	// Call the default editor runner and report errors to stderr (no return).
	if err := DefaultEditor(path); err != nil {
		// Avoid fmt import; write directly to stderr.
		_, _ = os.Stderr.WriteString("editor error: " + err.Error() + "\n")
	}
}
