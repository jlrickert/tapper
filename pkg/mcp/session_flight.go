package mcp

import (
	"context"
	"errors"
	"fmt"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

var errMCPFlightRequired = errors.New("no flight is selected; KEG tools are locked. Inspect flights through MCP with `list_flights` and `flight_show`, ask the user to select a flight in Tapper configuration, then orient again")

// failedOrientationPayload describes a selection that was made but could not be
// resolved. It deliberately does not reuse errMCPFlightRequired: reporting "no
// flight is selected" when one was selected and merely failed to resolve sends
// the reader looking for missing configuration instead of the real fault, which
// is usually a wrong flight name or an unreachable hub.
func failedOrientationPayload(err error) string {
	return "This session could not establish flight authority: " + err.Error() +
		"\n\nKEG tools are locked until it does. Call `list_flights` to see what" +
		" actually exists, then ask the user to correct the selected flight in" +
		" Tapper configuration and call `orient` again on this same connection." +
		" An empty flight list usually means this machine is not bootstrapped or" +
		" not authenticated to the hub that hosts the flight."
}

var recoveryToolNames = map[string]bool{
	"orient":       true,
	"list_flights": true,
	"flight_show":  true,
	"auth_info":    true,
}

// bootstrapToolNames is what a session running on the synthetic bootstrap
// flight may call. Its cover is empty, so the KEG tools would fail anyway;
// hiding them keeps the agent from spending the session discovering that one
// refusal at a time. Like recoveryToolNames this is an allowlist, so a KEG tool
// added later is hidden by default rather than leaking into bootstrap.
var bootstrapToolNames = map[string]bool{
	"orient":        true,
	"list_flights":  true,
	"flight_show":   true,
	"auth_info":     true,
	"flight_create": true,
	"flight_edit":   true,
	"flight_delete": true,
	"keg_create":    true,
}

// sessionMode is the authority state of one MCP session.
type sessionMode int

const (
	// modeActive: a real flight governs the session.
	modeActive sessionMode = iota
	// modeSelect: no flight is selected but flights exist to select. The agent
	// cannot fix this itself, so only the recovery tools are offered.
	modeSelect
	// modeBootstrap: no flight exists at all. A synthetic admin flight governs
	// the session so the agent can create the first flight and keg.
	modeBootstrap
)

var errMCPBootstrapOnly = errors.New("no flight is configured; this session is running on a temporary bootstrap flight and the KEG tools are locked. Create the first flight and KEG with `flight_create` and `keg_create`, ask the user to select the flight, then call `orient` again")

type flightSessionContextKey struct{}

// orientationContext is immutable after publication. Tool calls capture its
// pointer at their boundary, so an in-flight call finishes under the authority
// with which it began while later calls observe a successful refresh.
type orientationContext struct {
	flight   *tapper.Flight
	payload  string
	kegs     []tapper.OrientationKeg
	warnings []string
	mode     sessionMode
}

type flightSessionState struct {
	mu      sync.RWMutex
	current *orientationContext
}

type sessionFlightGate struct {
	provider OrientationProvider

	mu     sync.Mutex
	states map[string]*flightSessionState
	srv    *sdkmcp.Server
	calls  sync.RWMutex
}

func newSessionFlightGate(provider OrientationProvider) *sessionFlightGate {
	g := &sessionFlightGate{
		provider: provider,
		states:   map[string]*flightSessionState{},
	}
	return g
}

func (g *sessionFlightGate) state(sessionID string) *flightSessionState {
	if sessionID == "" {
		sessionID = "default"
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if state := g.states[sessionID]; state != nil {
		return state
	}
	state := &flightSessionState{}
	g.states[sessionID] = state
	return state
}

func (g *sessionFlightGate) current(sessionID string) *orientationContext {
	state := g.state(sessionID)
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.current
}

func (g *sessionFlightGate) refresh(ctx context.Context, sessionID string) (*orientationContext, error) {
	g.calls.Lock()
	defer g.calls.Unlock()
	candidate, err := g.provider.Load(ctx)
	if err != nil {
		// A failed explicit refresh retains the last valid authority.
		if current := g.current(sessionID); current != nil {
			return current, err
		}
		// Initialization must remain connectable for recovery.
		recovery := &orientationContext{payload: failedOrientationPayload(err), mode: modeSelect, warnings: []string{err.Error()}}
		state := g.state(sessionID)
		state.mu.Lock()
		state.current = recovery
		state.mu.Unlock()
		return recovery, err
	}
	if candidate == nil {
		candidate = &Orientation{}
	}
	next := &orientationContext{
		flight:   cloneFlight(candidate.Flight),
		payload:  candidate.Payload,
		kegs:     append([]tapper.OrientationKeg(nil), candidate.Kegs...),
		warnings: append([]string(nil), candidate.Warnings...),
		mode:     modeFor(candidate.Flight),
	}
	g.publish(sessionID, next)
	return next, nil
}

// modeFor classifies a loaded candidate. The provider decides *whether* to
// synthesize a bootstrap flight — it is the only layer that knows how to count
// its transport's flights — and the gate reads that decision off the manifest.
func modeFor(flight *tapper.Flight) sessionMode {
	switch {
	case flight == nil:
		return modeSelect
	case flight.Bootstrap:
		return modeBootstrap
	default:
		return modeActive
	}
}

func (g *sessionFlightGate) publish(sessionID string, next *orientationContext) {
	state := g.state(sessionID)
	state.mu.Lock()
	state.current = next
	state.mu.Unlock()
	g.notifyToolsChanged()
}

func cloneFlight(f *tapper.Flight) *tapper.Flight {
	if f == nil {
		return nil
	}
	out := *f
	out.Capabilities = append([]tapper.FlightCapability(nil), f.Capabilities...)
	out.Cover = append([]tapper.FlightCover(nil), f.Cover...)
	out.AllowedKegs = append([]string(nil), f.AllowedKegs...)
	return &out
}

func (g *sessionFlightGate) notifyToolsChanged() {
	if g.srv == nil {
		return
	}
	type markerInput struct{}
	sdkmcp.AddTool(g.srv, &sdkmcp.Tool{Name: "_orientation_transition_marker"}, func(context.Context, *sdkmcp.CallToolRequest, markerInput) (*sdkmcp.CallToolResult, any, error) {
		return textResult(""), nil, nil
	})
	g.srv.RemoveTools("_orientation_transition_marker")
}

// mode reports the session's authority state. An unseen session is treated as
// modeSelect: nothing has been published for it, so it has no flight and no
// evidence that creating one would help.
func (g *sessionFlightGate) mode(sessionID string) sessionMode {
	current := g.current(sessionID)
	if current == nil {
		return modeSelect
	}
	return current.mode
}

// allowedTools returns the allowlist governing sessionID, or nil when every
// registered tool is available.
func (g *sessionFlightGate) allowedTools(sessionID string) map[string]bool {
	switch g.mode(sessionID) {
	case modeSelect:
		return recoveryToolNames
	case modeBootstrap:
		return bootstrapToolNames
	default:
		return nil
	}
}

// lockedError explains why a tool outside the allowlist was refused. The two
// modes need different text: one asks the reader to pick an existing flight,
// the other to create the first one.
func (g *sessionFlightGate) lockedError(sessionID string) error {
	if g.mode(sessionID) == modeBootstrap {
		return errMCPBootstrapOnly
	}
	return errMCPFlightRequired
}

func (g *sessionFlightGate) activeFlight(ctx context.Context) *tapper.Flight {
	current := orientationFromContext(ctx)
	if current == nil {
		current = g.current(sessionIDFromContext(ctx))
	}
	if current == nil {
		return nil
	}
	return current.flight
}

func (g *sessionFlightGate) payload(ctx context.Context) string {
	current := orientationFromContext(ctx)
	if current == nil {
		current = g.current(sessionIDFromContext(ctx))
	}
	if current == nil {
		return ""
	}
	return current.payload
}

func (g *sessionFlightGate) canManage(sessionID string) bool {
	current := g.current(sessionID)
	return current != nil && current.flight != nil && current.flight.HasCapability(tapper.FlightCapabilityManageFlights)
}

func (g *sessionFlightGate) canManageKegs(sessionID string) bool {
	current := g.current(sessionID)
	return current != nil && current.flight != nil && current.flight.HasCapability(tapper.FlightCapabilityManageKegs)
}

func (g *sessionFlightGate) authorizeMutation(sessionID string) error {
	return g.authorizeCapability(sessionID, tapper.FlightCapabilityManageFlights)
}

func (g *sessionFlightGate) authorizeKegCreation(sessionID string) error {
	return g.authorizeCapability(sessionID, tapper.FlightCapabilityManageKegs)
}

func (g *sessionFlightGate) authorizeCapability(sessionID string, capability tapper.FlightCapability) error {
	current := g.current(sessionID)
	if current == nil || current.flight == nil {
		return errMCPFlightRequired
	}
	if !current.flight.HasCapability(capability) {
		return fmt.Errorf("active flight does not grant %s", capability)
	}
	return nil
}

func (g *sessionFlightGate) selfTarget(ctx context.Context, target string) (bool, error) {
	current := orientationFromContext(ctx)
	if current == nil || current.flight == nil {
		return false, errMCPFlightRequired
	}
	ref, err := tapper.ParseFlightRef(target, current.flight.Namespace)
	if err != nil {
		return false, err
	}
	return ref.Canonical() == current.flight.Name, nil
}

// adoptEditedFlight publishes the exact returned manifest after persistence.
// Flight mutation calls deliberately do not hold calls.RLock, so taking the
// write lock here waits for older in-flight calls without deadlocking itself.
func (g *sessionFlightGate) adoptEditedFlight(ctx context.Context, target string, flight *tapper.Flight) (bool, error) {
	self, err := g.selfTarget(ctx, target)
	if err != nil || !self {
		return self, err
	}
	g.calls.Lock()
	defer g.calls.Unlock()
	candidate, renderErr := g.provider.Render(ctx, cloneFlight(flight))
	if renderErr != nil {
		warning := "flight update was applied, but orientation refresh failed: " + renderErr.Error()
		g.publish(sessionIDFromContext(ctx), &orientationContext{
			payload: errMCPFlightRequired.Error(), warnings: []string{warning}, mode: modeSelect,
		})
		return true, errors.New(warning)
	}
	if candidate == nil {
		candidate = &Orientation{}
	}
	next := &orientationContext{
		flight: cloneFlight(flight), payload: candidate.Payload,
		kegs:     append([]tapper.OrientationKeg(nil), candidate.Kegs...),
		warnings: append([]string(nil), candidate.Warnings...),
		mode:     modeFor(flight),
	}
	g.publish(sessionIDFromContext(ctx), next)
	return true, nil
}

func (g *sessionFlightGate) adoptDeletedFlight(ctx context.Context, target string) (bool, error) {
	self, err := g.selfTarget(ctx, target)
	if err != nil || !self {
		return self, err
	}
	g.calls.Lock()
	defer g.calls.Unlock()
	// No agent name: this is the self-deletion path, where the flight the
	// session was running on has just been removed. The gate has no Tap to ask,
	// and "your flight is gone" is the whole message.
	payload, payloadErr := tapper.BuildOrientationPayload(nil, "", "", nil, nil)
	if payloadErr != nil {
		payload = errMCPFlightRequired.Error()
	}
	g.publish(sessionIDFromContext(ctx), &orientationContext{payload: payload, mode: modeSelect})
	return true, nil
}

func sessionIDFromRequest(req sdkmcp.Request) string {
	if req == nil || req.GetSession() == nil {
		return "default"
	}
	if session, ok := req.GetSession().(*sdkmcp.ServerSession); ok {
		return serverSessionID(session)
	}
	return "default"
}

func serverSessionID(session *sdkmcp.ServerSession) string {
	if session == nil {
		return "default"
	}
	if id := session.ID(); id != "" {
		return id
	}
	return fmt.Sprintf("session:%p", session)
}

func sessionIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(flightSessionContextKey{}).(string)
	if id == "" {
		return "default"
	}
	return id
}

func orientationFromContext(ctx context.Context) *orientationContext {
	current, _ := ctx.Value(orientationContextKey{}).(*orientationContext)
	return current
}

// SessionFlight returns the immutable flight snapshot captured for the current
// MCP tool-call boundary. Hosted discovery tools use it to apply the same
// cover as KEG operations without reaching into session storage.
func SessionFlight(ctx context.Context) *tapper.Flight {
	current := orientationFromContext(ctx)
	if current == nil {
		return nil
	}
	return cloneFlight(current.flight)
}

// HasSessionOrientation reports whether the current call is governed by a
// session orientation gate. A governed recovery session has no flight but
// still returns true; ungated embedded surfaces return false.
func HasSessionOrientation(ctx context.Context) bool {
	return orientationFromContext(ctx) != nil
}

type orientationContextKey struct{}

func (g *sessionFlightGate) middleware(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		sessionID := sessionIDFromRequest(req)
		ctx = context.WithValue(ctx, flightSessionContextKey{}, sessionID)

		if method == "initialize" {
			current, refreshErr := g.refresh(ctx, sessionID)
			result, err := next(context.WithValue(ctx, orientationContextKey{}, current), method, req)
			if err != nil {
				return result, err
			}
			if initialized, ok := result.(*sdkmcp.InitializeResult); ok {
				copyResult := *initialized
				copyResult.Instructions = current.payload
				if refreshErr != nil {
					copyResult.Instructions += "\n\nRecovery warning: " + refreshErr.Error()
				}
				return &copyResult, nil
			}
			return result, nil
		}

		current := g.current(sessionID)
		ctx = context.WithValue(ctx, orientationContextKey{}, current)
		if method == "tools/list" {
			result, err := next(ctx, method, req)
			if err != nil {
				return result, err
			}
			listed, ok := result.(*sdkmcp.ListToolsResult)
			if !ok {
				return result, nil
			}
			copyResult := *listed
			copyResult.Tools = make([]*sdkmcp.Tool, 0, len(listed.Tools))
			allowed := g.allowedTools(sessionID)
			canManage, canManageKegs := g.canManage(sessionID), g.canManageKegs(sessionID)
			for _, tool := range listed.Tools {
				if allowed != nil && !allowed[tool.Name] {
					continue
				}
				if isFlightMutationTool(tool.Name) && !canManage {
					continue
				}
				if isKegCreationTool(tool.Name) && !canManageKegs {
					continue
				}
				copyResult.Tools = append(copyResult.Tools, tool)
			}
			return &copyResult, nil
		}
		if method == "tools/call" {
			params, _ := req.GetParams().(*sdkmcp.CallToolParamsRaw)
			if params != nil && params.Name == "orient" {
				return next(ctx, method, req)
			}
			if allowed := g.allowedTools(sessionID); params != nil && allowed != nil && !allowed[params.Name] {
				return errorResult(g.lockedError(sessionID)), nil
			}
			if params != nil && isFlightMutationTool(params.Name) {
				if err := g.authorizeMutation(sessionID); err != nil {
					return errorResult(err), nil
				}
				return next(ctx, method, req)
			}
			if params != nil && isKegCreationTool(params.Name) {
				if err := g.authorizeKegCreation(sessionID); err != nil {
					return errorResult(err), nil
				}
				return next(ctx, method, req)
			}
			g.calls.RLock()
			defer g.calls.RUnlock()
		}
		if method == "resources/read" || method == "resources/subscribe" {
			if params, ok := req.GetParams().(*sdkmcp.ReadResourceParams); ok && params.URI == orientResourceURI {
				return next(ctx, method, req)
			}
			g.calls.RLock()
			defer g.calls.RUnlock()
			// Node resources are KEG reads, so bootstrap locks them exactly as
			// it locks the KEG tools.
			if g.mode(sessionID) != modeActive {
				return nil, g.lockedError(sessionID)
			}
		}
		return next(ctx, method, req)
	}
}

func isFlightMutationTool(name string) bool {
	return name == "flight_create" || name == "flight_edit" || name == "flight_delete"
}

func isKegCreationTool(name string) bool {
	return name == "keg_create"
}
