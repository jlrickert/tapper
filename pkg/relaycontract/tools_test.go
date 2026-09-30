package relaycontract

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func validTools() Tools {
	readOnly := true
	return Tools{Servers: []ToolServer{{
		Name:          "everything",
		MaxConcurrent: 4,
		Tools: []Tool{{
			Name:        "echo",
			Description: "Echoes its input.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"message":{"type":"string"}}}`),
			Annotations: &ToolAnnotations{ReadOnlyHint: &readOnly},
		}},
	}}}
}

func TestToolsRoundTrip(t *testing.T) {
	env, err := NewEnvelope(TypeTools, "", validTools())
	if err != nil {
		t.Fatal(err)
	}
	if env.V != EnvelopeVersion {
		t.Fatalf("envelope V = %d, want %d", env.V, EnvelopeVersion)
	}
	var back Tools
	if err := env.Decode(&back); err != nil {
		t.Fatal(err)
	}
	a := back.Servers[0].Tools[0].Annotations
	if a == nil || a.ReadOnlyHint == nil || !*a.ReadOnlyHint || a.DestructiveHint != nil {
		t.Fatalf("annotations = %+v", a)
	}
}

func TestToolsValidate(t *testing.T) {
	cases := map[string]func(*Tools){
		"bad server name":   func(x *Tools) { x.Servers[0].Name = "a/b" },
		"zero concurrency":  func(x *Tools) { x.Servers[0].MaxConcurrent = 0 },
		"huge concurrency":  func(x *Tools) { x.Servers[0].MaxConcurrent = MaxConcurrentCap + 1 },
		"duplicate server":  func(x *Tools) { x.Servers = append(x.Servers, x.Servers[0]) },
		"duplicate tool":    func(x *Tools) { x.Servers[0].Tools = append(x.Servers[0].Tools, x.Servers[0].Tools[0]) },
		"bad tool name":     func(x *Tools) { x.Servers[0].Tools[0].Name = "has space" },
		"long tool name":    func(x *Tools) { x.Servers[0].Tools[0].Name = strings.Repeat("a", MaxToolNameLength+1) },
		"long description":  func(x *Tools) { x.Servers[0].Tools[0].Description = strings.Repeat("a", MaxToolDescription+1) },
		"schema not object": func(x *Tools) { x.Servers[0].Tools[0].InputSchema = json.RawMessage(`[]`) },
		"schema missing":    func(x *Tools) { x.Servers[0].Tools[0].InputSchema = nil },
		"schema too large": func(x *Tools) {
			x.Servers[0].Tools[0].InputSchema = json.RawMessage(`{"d":"` + strings.Repeat("a", MaxToolSchemaBytes) + `"}`)
		},
		"too many servers": func(x *Tools) {
			for i := 0; i < MaxToolServers; i++ {
				s := x.Servers[0]
				s.Name = fmt.Sprintf("s%d", i)
				x.Servers = append(x.Servers, s)
			}
		},
		"too many tools on a server": func(x *Tools) {
			base := x.Servers[0].Tools[0]
			for i := 0; i < MaxToolsPerServer; i++ {
				tool := base
				tool.Name = fmt.Sprintf("t%d", i)
				x.Servers[0].Tools = append(x.Servers[0].Tools, tool)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			tools := validTools()
			mutate(&tools)
			if err := tools.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	tools := validTools()
	if err := tools.Validate(); err != nil {
		t.Fatalf("valid tools rejected: %v", err)
	}
}

func TestToolsValidateTotalLimit(t *testing.T) {
	var tools Tools
	base := validTools().Servers[0]
	for i := 0; i*MaxToolsPerServer <= MaxTools; i++ {
		s := base
		s.Name = fmt.Sprintf("s%d", i)
		s.Tools = nil
		for j := 0; j < MaxToolsPerServer; j++ {
			tool := base.Tools[0]
			tool.Name = fmt.Sprintf("t%d", j)
			s.Tools = append(s.Tools, tool)
		}
		tools.Servers = append(tools.Servers, s)
	}
	if err := tools.Validate(); err == nil || !strings.Contains(err.Error(), "tools may be offered") {
		t.Fatalf("Validate = %v, want total tool limit error", err)
	}
}

func TestCallValidate(t *testing.T) {
	valid := func() Call {
		return Call{Server: "everything", Tool: "echo", Arguments: json.RawMessage(`{"message":"hi"}`)}
	}
	cases := map[string]func(*Call){
		"bad server":      func(c *Call) { c.Server = "" },
		"bad tool":        func(c *Call) { c.Tool = "a b" },
		"args not object": func(c *Call) { c.Arguments = json.RawMessage(`"x"`) },
		"args missing":    func(c *Call) { c.Arguments = nil },
		"args too large": func(c *Call) {
			c.Arguments = json.RawMessage(`{"d":"` + strings.Repeat("a", MaxToolCallArgsBytes) + `"}`)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := valid()
			mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	c := valid()
	if err := c.Validate(); err != nil {
		t.Fatalf("valid call rejected: %v", err)
	}
}

func TestCallRejectsUnknownFields(t *testing.T) {
	env := Envelope{V: EnvelopeVersion, Type: TypeCall, Payload: json.RawMessage(`{"server":"s","tool":"t","arguments":{},"command":"rm -rf /"}`)}
	var c Call
	if err := env.Decode(&c); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}
