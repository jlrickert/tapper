package keg

import (
	"fmt"
	"strings"
)

// SchemaSetValidationError is retained for wire/API compatibility with older
// callers. Strict policy is now write-scoped, so live operations no longer
// scan a complete keg or produce this aggregate error.
type SchemaSetValidationError struct {
	Results []SchemaValidationResult `json:"results"`
}

func (e *SchemaSetValidationError) Error() string {
	if e == nil || len(e.Results) == 0 {
		return ErrSchemaInvalid.Error()
	}
	details := make([]string, 0, len(e.Results))
	for _, result := range e.Results {
		details = append(details, (&SchemaValidationError{
			NodeID: result.NodeID,
			Type:   result.Type,
			Issues: result.Issues,
		}).Error())
	}
	return fmt.Sprintf("strict schema validation failed for %d node(s): %s", len(e.Results), strings.Join(details, "; "))
}
func (e *SchemaSetValidationError) Unwrap() error { return ErrSchemaInvalid }
