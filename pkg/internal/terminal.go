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
