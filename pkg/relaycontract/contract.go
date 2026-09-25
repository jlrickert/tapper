// Package relaycontract defines the wire protocol spoken between `tap relay`
// and Tapper Hub. A relay dials Hub, registers the models it can serve, and
// then answers inference requests Hub pushes down the same connection.
//
// Every frame is an Envelope. Payloads are decoded strictly: an unknown field
// is an error rather than something forwarded to a provider.
package relaycontract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ProtocolVersion is the newest relay protocol this package speaks.
const ProtocolVersion = 1

// ConnectPath is the Hub route a relay dials, relative to the hub base URL.
const ConnectPath = "/api/v1/relay/connect"

// Frame types.
const (
	TypeRegister   = "register"
	TypeRegistered = "registered"
	TypeCatalog    = "catalog"
	TypePing       = "ping"
	TypePong       = "pong"
	TypeInfer      = "infer"
	TypeChunk      = "chunk"
	TypeDone       = "done"
	TypeError      = "error"
	TypeCancel     = "cancel"
)

// APIOpenAIChatCompletions is the chat inference API. The request body is an
// OpenAI chat completions request; each chunk is one OpenAI completion or
// completion-chunk object.
const APIOpenAIChatCompletions = "openai.chat.completions"

// APIOpenAIAudioTranscriptions turns recorded speech into text. The request
// body is a TranscriptionRequest; the relay answers with exactly one chunk,
// a TranscriptionResult, then done. Hub only sends it to models advertising
// CapabilityTranscription, so a relay that predates it never sees one.
const APIOpenAIAudioTranscriptions = "openai.audio.transcriptions"

// Model capabilities a relay advertises.
const (
	CapabilityChat          = "chat"
	CapabilityStream        = "stream"
	CapabilityTranscription = "transcription"
)

// MaxTranscriptionAudioBytes bounds one recording. Base64 in a JSON frame, it
// stays well inside the 16 MiB frame limit both ends enforce.
const MaxTranscriptionAudioBytes = 10 << 20

// Error codes carried by an Error payload.
const (
	CodeBadRequest          = "bad_request"
	CodeUnsupported         = "unsupported"
	CodeUnknownModel        = "unknown_model"
	CodeProviderUnavailable = "provider_unavailable"
	CodeProviderError       = "provider_error"
	CodeOverloaded          = "overloaded"
	CodeCancelled           = "cancelled"
	CodeRelayDisconnected   = "relay_disconnected"
	CodeInternal            = "internal"
	// CodeDisconnected ends a session because the relay's owner disconnected
	// it from Hub. It is connection-level (no id) and the relay must not
	// reconnect: the owner asked for it to go away.
	CodeDisconnected = "disconnected"
)

// Limits on what a relay may advertise.
const (
	MaxNameLength    = 64
	MaxModelIDLength = 200
	MaxModels        = 256
	MaxConcurrentCap = 64
	// MaxPriority is the lowest-ranked priority a model may carry.
	MaxPriority = 99
)

// Envelope is the frame shared by every message. ID correlates an infer
// request with its chunks and terminal frame; it is empty for connection-level
// frames such as register and ping.
type Envelope struct {
	V       int             `json:"v"`
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewEnvelope marshals payload into an envelope of the given type.
func NewEnvelope(typ, id string, payload any) (Envelope, error) {
	env := Envelope{V: ProtocolVersion, ID: id, Type: typ}
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return Envelope{}, fmt.Errorf("relaycontract: encode %s: %w", typ, err)
		}
		env.Payload = raw
	}
	return env, nil
}

// Decode strictly unmarshals the envelope payload into dst and validates it
// when dst implements Validate.
func (e Envelope) Decode(dst any) error {
	if len(e.Payload) == 0 {
		return fmt.Errorf("relaycontract: %s frame has no payload", e.Type)
	}
	dec := json.NewDecoder(bytes.NewReader(e.Payload))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("relaycontract: decode %s: %w", e.Type, err)
	}
	if dec.More() {
		return fmt.Errorf("relaycontract: decode %s: trailing data", e.Type)
	}
	if v, ok := dst.(interface{ Validate() error }); ok {
		if err := v.Validate(); err != nil {
			return fmt.Errorf("relaycontract: invalid %s: %w", e.Type, err)
		}
	}
	return nil
}

// Register is the first frame a relay sends.
type Register struct {
	Relay  RelayInfo `json:"relay"`
	Limits Limits    `json:"limits"`
	Models []Model   `json:"models"`
}

// RelayInfo identifies the relay process.
type RelayInfo struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Protocols []int  `json:"protocols"`
}

// Limits bounds how much work Hub may push at the relay at once.
type Limits struct {
	MaxConcurrent int `json:"maxConcurrent"`
}

// Model is one model a relay offers. ID is the provider's own model name.
// Priority is the relay owner's ranking, 1 to MaxPriority with lower
// preferred; 0 is unranked and sorts after every ranked model. Hub lists
// models and routes pool requests in that order.
type Model struct {
	ID            string   `json:"id"`
	Provider      string   `json:"provider"`
	ContextWindow int      `json:"contextWindow,omitempty"`
	Capabilities  []string `json:"capabilities,omitempty"`
	Priority      int      `json:"priority,omitempty"`
}

// Registered is Hub's answer to Register.
type Registered struct {
	Protocol int              `json:"protocol"`
	Models   []CatalogBinding `json:"models"`
}

// CatalogBinding maps a relay's model to the catalog id Hub assigned it.
type CatalogBinding struct {
	CatalogID string `json:"catalogId"`
	Provider  string `json:"provider"`
	Model     string `json:"model"`
}

// Catalog replaces the relay's advertised model list after registration.
type Catalog struct {
	Models []Model `json:"models"`
}

// Infer is one inference request pushed from Hub to a relay.
type Infer struct {
	API      string          `json:"api"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Stream   bool            `json:"stream"`
	Body     json.RawMessage `json:"body"`
}

// TranscriptionRequest is the body of an APIOpenAIAudioTranscriptions infer.
// Audio is the recording's bytes (base64 on the wire).
type TranscriptionRequest struct {
	Audio    []byte `json:"audio"`
	MimeType string `json:"mimeType"`
	Filename string `json:"filename,omitempty"`
	Language string `json:"language,omitempty"`
	Prompt   string `json:"prompt,omitempty"`
}

// TranscriptionResult is the one chunk answering a transcription.
type TranscriptionResult struct {
	Text string `json:"text"`
}

// Chunk carries one provider response object for an in-flight request. For a
// non-streaming request the relay sends exactly one chunk holding the whole
// response.
type Chunk struct {
	Data json.RawMessage `json:"data"`
}

// Done terminates a request successfully.
type Done struct {
	Usage *Usage `json:"usage,omitempty"`
}

// Usage counts tokens when the provider reports them.
type Usage struct {
	PromptTokens     int `json:"promptTokens,omitempty"`
	CompletionTokens int `json:"completionTokens,omitempty"`
}

// Error terminates a request, or the connection when the envelope has no ID.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Validate checks the register frame against protocol limits.
func (r *Register) Validate() error {
	if err := validateName("relay.name", r.Relay.Name); err != nil {
		return err
	}
	if len(r.Relay.Protocols) == 0 {
		return errors.New("relay.protocols is empty")
	}
	if r.Limits.MaxConcurrent < 1 || r.Limits.MaxConcurrent > MaxConcurrentCap {
		return fmt.Errorf("limits.maxConcurrent must be between 1 and %d", MaxConcurrentCap)
	}
	return validateModels(r.Models)
}

// Validate checks the catalog frame against protocol limits.
func (c *Catalog) Validate() error { return validateModels(c.Models) }

// Validate checks the infer frame.
func (i *Infer) Validate() error {
	if i.API != APIOpenAIChatCompletions && i.API != APIOpenAIAudioTranscriptions {
		return fmt.Errorf("unsupported api %q", i.API)
	}
	if err := validateName("provider", i.Provider); err != nil {
		return err
	}
	if i.Model == "" || len(i.Model) > MaxModelIDLength {
		return errors.New("model is empty or too long")
	}
	if !isJSONObject(i.Body) {
		return errors.New("body must be a JSON object")
	}
	return nil
}

// Validate checks a transcription request.
func (t *TranscriptionRequest) Validate() error {
	if len(t.Audio) == 0 {
		return errors.New("audio is empty")
	}
	if len(t.Audio) > MaxTranscriptionAudioBytes {
		return fmt.Errorf("audio is larger than %d bytes", MaxTranscriptionAudioBytes)
	}
	if t.MimeType == "" || len(t.MimeType) > 100 {
		return errors.New("mimeType is empty or too long")
	}
	if len(t.Filename) > 200 || len(t.Language) > 35 || len(t.Prompt) > 4000 {
		return errors.New("filename, language, or prompt is too long")
	}
	return nil
}

// Validate checks the chunk frame.
func (c *Chunk) Validate() error {
	if !isJSONObject(c.Data) {
		return errors.New("data must be a JSON object")
	}
	return nil
}

// Validate checks the error frame.
func (e *Error) Validate() error {
	if e.Code == "" {
		return errors.New("code is empty")
	}
	return nil
}

// SelectProtocol picks the highest version both sides speak, or 0.
func SelectProtocol(offered []int) int {
	best := 0
	for _, v := range offered {
		if v >= 1 && v <= ProtocolVersion && v > best {
			best = v
		}
	}
	return best
}

func validateModels(models []Model) error {
	if len(models) > MaxModels {
		return fmt.Errorf("at most %d models may be offered", MaxModels)
	}
	seen := make(map[string]struct{}, len(models))
	for i, m := range models {
		if m.ID == "" || len(m.ID) > MaxModelIDLength || strings.ContainsAny(m.ID, "\x00\n\r") {
			return fmt.Errorf("models[%d].id is empty, too long, or contains control characters", i)
		}
		if err := validateName(fmt.Sprintf("models[%d].provider", i), m.Provider); err != nil {
			return err
		}
		if m.Priority < 0 || m.Priority > MaxPriority {
			return fmt.Errorf("models[%d].priority must be between 0 and %d", i, MaxPriority)
		}
		key := m.Provider + "/" + m.ID
		if _, dup := seen[key]; dup {
			return fmt.Errorf("models[%d] duplicates %s", i, key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

// validateName accepts short names made of letters, digits, '.', '_' and '-'.
func validateName(field, name string) error {
	if name == "" || len(name) > MaxNameLength {
		return fmt.Errorf("%s must be 1-%d characters", field, MaxNameLength)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
		default:
			return fmt.Errorf("%s may contain only letters, digits, '.', '_' and '-'", field)
		}
	}
	return nil
}

// ValidName reports whether name is acceptable as a relay or provider name.
func ValidName(name string) bool { return validateName("name", name) == nil }

func isJSONObject(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	return json.Valid(trimmed)
}
