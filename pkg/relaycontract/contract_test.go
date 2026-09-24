package relaycontract

import (
	"encoding/json"
	"strings"
	"testing"
)

func validRegister() Register {
	return Register{
		Relay:  RelayInfo{Name: "laptop", Version: "0.43.0", Protocols: []int{1}},
		Limits: Limits{MaxConcurrent: 4},
		Models: []Model{{ID: "qwen3:8b", Provider: "ollama", Capabilities: []string{"chat", "stream"}}},
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	env, err := NewEnvelope(TypeRegister, "", validRegister())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var back Envelope
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back.V != ProtocolVersion || back.Type != TypeRegister {
		t.Fatalf("envelope header = %+v", back)
	}
	var reg Register
	if err := back.Decode(&reg); err != nil {
		t.Fatal(err)
	}
	if reg.Relay.Name != "laptop" || len(reg.Models) != 1 || reg.Models[0].ID != "qwen3:8b" {
		t.Fatalf("register = %+v", reg)
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	env := Envelope{V: 1, Type: TypeInfer, Payload: json.RawMessage(`{"api":"openai.chat.completions","provider":"ollama","model":"m","stream":false,"body":{},"url":"http://evil"}`)}
	var in Infer
	if err := env.Decode(&in); err == nil {
		t.Fatal("expected unknown field to be rejected")
	}
}

func TestRegisterValidate(t *testing.T) {
	cases := map[string]func(*Register){
		"empty name":       func(r *Register) { r.Relay.Name = "" },
		"bad name":         func(r *Register) { r.Relay.Name = "a b" },
		"no protocols":     func(r *Register) { r.Relay.Protocols = nil },
		"zero concurrency": func(r *Register) { r.Limits.MaxConcurrent = 0 },
		"huge concurrency": func(r *Register) { r.Limits.MaxConcurrent = MaxConcurrentCap + 1 },
		"empty model id":   func(r *Register) { r.Models[0].ID = "" },
		"model newline":    func(r *Register) { r.Models[0].ID = "a\nb" },
		"bad provider":     func(r *Register) { r.Models[0].Provider = "a/b" },
		"duplicate model":  func(r *Register) { r.Models = append(r.Models, r.Models[0]) },
		"long model id":    func(r *Register) { r.Models[0].ID = strings.Repeat("x", MaxModelIDLength+1) },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := validRegister()
			mutate(&r)
			if err := r.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	r := validRegister()
	if err := r.Validate(); err != nil {
		t.Fatalf("valid register rejected: %v", err)
	}
}

func TestInferValidate(t *testing.T) {
	ok := Infer{API: APIOpenAIChatCompletions, Provider: "ollama", Model: "m", Body: json.RawMessage(`{"messages":[]}`)}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid infer rejected: %v", err)
	}
	bad := ok
	bad.API = "shell"
	if err := bad.Validate(); err == nil {
		t.Fatal("expected unsupported api to be rejected")
	}
	bad = ok
	bad.Body = json.RawMessage(`[1]`)
	if err := bad.Validate(); err == nil {
		t.Fatal("expected non-object body to be rejected")
	}
}

func TestSelectProtocol(t *testing.T) {
	if got := SelectProtocol([]int{1, 2, 3}); got != 1 {
		t.Fatalf("SelectProtocol = %d, want 1", got)
	}
	if got := SelectProtocol([]int{2}); got != 0 {
		t.Fatalf("SelectProtocol = %d, want 0", got)
	}
}
