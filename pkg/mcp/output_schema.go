package mcp

import (
	"fmt"
	"reflect"

	"github.com/google/jsonschema-go/jsonschema"
)

// Every tool declares an outputSchema for its structuredContent, so a client
// (and the Hub's tool inspector) can see a result's shape before calling.
//
// publicResponseMiddleware shapes every result the same way: the tool's own
// structured fields, plus message, the result's text. A tool whose result is
// only text therefore still returns an object, {"message": "..."}, and
// declares exactly that (textOutputSchema) rather than nothing.
//
// The schemas leave additionalProperties open. They document the fields a
// client may rely on; a newer server adding a field must not make an older
// client that validates results reject them.

// outputMessageDescription describes the message field every result carries.
const outputMessageDescription = "The result as text, for clients that read text. For a tool with no other fields, it is the whole result."

// textOutput is the structured content of a tool whose result is only text.
type textOutput struct {
	Message string `json:"message" jsonschema:"The result as text."`
}

// textOutputSchema is the outputSchema of a tool whose result is only text.
func textOutputSchema() *jsonschema.Schema {
	return outputSchema[textOutput]()
}

// outputSchema derives a tool's outputSchema from T, the type its handler
// puts in StructuredContent, and adds the message field the public response
// adds to every result.
func outputSchema[T any]() *jsonschema.Schema {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("derive MCP output schema for %T: %v", *new(T), err))
	}
	openSchema(schema)
	if schema.Properties == nil {
		schema.Properties = map[string]*jsonschema.Schema{}
	}
	if _, ok := schema.Properties["message"]; !ok {
		schema.Properties["message"] = &jsonschema.Schema{Type: "string", Description: outputMessageDescription}
	}
	return schema
}

// openSchema removes the additionalProperties: false that schema inference
// puts on every struct, throughout schema.
func openSchema(schema *jsonschema.Schema) {
	if schema == nil {
		return
	}
	if isFalseSchema(schema.AdditionalProperties) {
		schema.AdditionalProperties = nil
	} else {
		openSchema(schema.AdditionalProperties)
	}
	for _, property := range schema.Properties {
		openSchema(property)
	}
	openSchema(schema.Items)
	for _, def := range schema.Defs {
		openSchema(def)
	}
	for _, alt := range schema.AnyOf {
		openSchema(alt)
	}
	for _, alt := range schema.OneOf {
		openSchema(alt)
	}
}

func isFalseSchema(s *jsonschema.Schema) bool {
	return s != nil && reflect.DeepEqual(s, &jsonschema.Schema{Not: &jsonschema.Schema{}})
}
