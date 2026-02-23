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
