package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func renderUserError(err error, deps *Deps) string {
	if err == nil {
		return ""
	}

	var projectErr *tapper.ProjectKegNotFoundError
	if errors.As(err, &projectErr) {
		if isDebugLogLevel(deps) && len(projectErr.Tried) > 0 {
			return fmt.Sprintf("project keg not found in this project (searched: %s)", strings.Join(projectErr.Tried, ", "))
		}
		return "project keg not found in this project"
	}

	return err.Error()
}

func isDebugLogLevel(deps *Deps) bool {
	if deps == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(deps.LogLevel), "debug")
}
