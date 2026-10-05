package relaycontract

import (
	"encoding/json"
	"errors"
	"fmt"
)

// Limits on the MCP servers and tools a relay may advertise, and on the calls
// that reach them. A relay drops a tool that breaks one of these before it
// sends the tools frame, so one bad tool never costs the whole catalog.
const (
	MaxToolServers       = 32
	MaxToolsPerServer    = 128
	MaxTools             = 512
	MaxToolNameLength    = 128
	MaxToolTitleLength   = 200
	MaxToolDescription   = 4 << 10
	MaxToolSchemaBytes   = 64 << 10
	MaxToolsFrameBytes   = 4 << 20
	MaxToolCallArgsBytes = 1 << 20
	MaxToolResultBytes   = 4 << 20
)

// Tools replaces every MCP server a relay offers. Hub treats it as the whole
// list: a server missing from it is gone.
type Tools struct {
	Servers []ToolServer `json:"servers"`
}

// ToolServer is one MCP server from the relay owner's configuration. Name is
// the key the owner gave it, not anything the server reports. MaxConcurrent
// bounds this server's in-flight calls across every hub the relay serves.
type ToolServer struct {
	Name          string `json:"name"`
	Title         string `json:"title,omitempty"`
	MaxConcurrent int    `json:"maxConcurrent"`
	Tools         []Tool `json:"tools"`
}

// Tool is one tool as its MCP server lists it. InputSchema is the tool's JSON
// Schema for its arguments; Annotations are the server's own hints, passed
// through as the server reported them.
type Tool struct {
	Name        string           `json:"name"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	InputSchema json.RawMessage  `json:"inputSchema"`
	Annotations *ToolAnnotations `json:"annotations,omitempty"`
}

// ToolAnnotations mirrors MCP's tool annotations. Nil means the server did not
// say.
type ToolAnnotations struct {
	ReadOnlyHint    *bool `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool `json:"openWorldHint,omitempty"`
}

// Call is one tool call pushed from Hub to a relay. Server and Tool name an
// entry from the relay's own tools frame; nothing in a call can name a
// command, URL, header, or environment variable.
type Call struct {
	Server    string          `json:"server"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

// Validate checks the tools frame against protocol limits.
func (t *Tools) Validate() error {
	if len(t.Servers) > MaxToolServers {
		return fmt.Errorf("at most %d tool servers may be offered", MaxToolServers)
	}
	total := 0
	seen := make(map[string]struct{}, len(t.Servers))
	for i := range t.Servers {
		s := &t.Servers[i]
		if err := s.validate(fmt.Sprintf("servers[%d]", i)); err != nil {
			return err
		}
		if _, dup := seen[s.Name]; dup {
			return fmt.Errorf("servers[%d] duplicates %s", i, s.Name)
		}
		seen[s.Name] = struct{}{}
		total += len(s.Tools)
	}
	if total > MaxTools {
		return fmt.Errorf("at most %d tools may be offered", MaxTools)
	}
	return nil
}

func (s *ToolServer) validate(field string) error {
	if err := validateName(field+".name", s.Name); err != nil {
		return err
	}
	if len(s.Title) > MaxToolTitleLength {
		return fmt.Errorf("%s.title is longer than %d bytes", field, MaxToolTitleLength)
	}
	if s.MaxConcurrent < 1 || s.MaxConcurrent > MaxConcurrentCap {
		return fmt.Errorf("%s.maxConcurrent must be between 1 and %d", field, MaxConcurrentCap)
	}
	if len(s.Tools) > MaxToolsPerServer {
		return fmt.Errorf("%s offers more than %d tools", field, MaxToolsPerServer)
	}
	seen := make(map[string]struct{}, len(s.Tools))
	for j := range s.Tools {
		tf := fmt.Sprintf("%s.tools[%d]", field, j)
		if err := s.Tools[j].Validate(); err != nil {
			return fmt.Errorf("%s: %w", tf, err)
		}
		if _, dup := seen[s.Tools[j].Name]; dup {
			return fmt.Errorf("%s duplicates %s", tf, s.Tools[j].Name)
		}
		seen[s.Tools[j].Name] = struct{}{}
	}
	return nil
}

// Validate checks one tool against protocol limits. A relay runs it on each
// tool it lists so it can drop the ones Hub would refuse.
func (t *Tool) Validate() error {
	if !ValidToolName(t.Name) {
		return fmt.Errorf("name %q must be 1-%d letters, digits, '.', '_' or '-'", t.Name, MaxToolNameLength)
	}
	if len(t.Title) > MaxToolTitleLength {
		return fmt.Errorf("title is longer than %d bytes", MaxToolTitleLength)
	}
	if len(t.Description) > MaxToolDescription {
		return fmt.Errorf("description is longer than %d bytes", MaxToolDescription)
	}
	if len(t.InputSchema) > MaxToolSchemaBytes {
		return fmt.Errorf("inputSchema is larger than %d bytes", MaxToolSchemaBytes)
	}
	if !isJSONObject(t.InputSchema) {
		return errors.New("inputSchema must be a JSON object")
	}
	return nil
}

// Validate checks the call frame.
func (c *Call) Validate() error {
	if err := validateName("server", c.Server); err != nil {
		return err
	}
	if !ValidToolName(c.Tool) {
		return fmt.Errorf("tool %q is not a valid tool name", c.Tool)
	}
	if len(c.Arguments) > MaxToolCallArgsBytes {
		return fmt.Errorf("arguments are larger than %d bytes", MaxToolCallArgsBytes)
	}
	if !isJSONObject(c.Arguments) {
		return errors.New("arguments must be a JSON object")
	}
	return nil
}

// ValidToolName reports whether name is acceptable as a tool name: 1 to
// MaxToolNameLength letters, digits, '.', '_' or '-'. MCP allows more; a relay
// drops a tool whose name falls outside this set.
func ValidToolName(name string) bool {
	if name == "" || len(name) > MaxToolNameLength {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return false
		}
	}
	return true
}
