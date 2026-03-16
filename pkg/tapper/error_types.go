package tapper

import (
	"fmt"
	"strings"
)

// ProjectKegNotFoundError indicates project-local keg discovery failed.
// Tried contains the concrete keg-file locations that were checked.
type ProjectKegNotFoundError struct {
	Tried []string
}

func (e *ProjectKegNotFoundError) Error() string {
	if e == nil {
		return "project keg not found"
	}
	switch len(e.Tried) {
	case 0:
		return "project keg not found"
	case 1:
		return fmt.Sprintf("project keg not found; expected a `keg` file at %s", e.Tried[0])
	default:
		return fmt.Sprintf("project keg not found; expected a `keg` file at %s or %s", e.Tried[0], e.Tried[1])
	}
}

// UserMessage returns a CLI-context-aware message. When debug is true and
// search paths are available, they are included in the output.
func (e *ProjectKegNotFoundError) UserMessage(debug bool) string {
	if debug && len(e.Tried) > 0 {
		return fmt.Sprintf("project keg not found in this project (searched: %s)", strings.Join(e.Tried, ", "))
	}
	return "project keg not found in this project"
}

func newProjectKegNotFoundError(paths []string) error {
	cleaned := make([]string, 0, len(paths))
	for _, p := range paths {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		cleaned = append(cleaned, p)
	}
	return &ProjectKegNotFoundError{Tried: cleaned}
}

// PathNotFoundError indicates that the explicit --path target does not exist on disk.
type PathNotFoundError struct {
	Path string
}

func (e *PathNotFoundError) Error() string {
	return fmt.Sprintf("keg not found at path %q: directory does not exist", e.Path)
}
