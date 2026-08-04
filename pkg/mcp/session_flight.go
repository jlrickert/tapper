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

var recoveryToolNames = map[string]bool{
	"orient":       true,
	"list_flights": true,
	"flight_show":  true,
	"auth_info":    true,
}

type flightSessionContextKey struct{}

// orientationContext is immutable after publication. Tool calls capture its
// pointer at their boundary, so an in-flight call finishes under the authority
// with which it began while later calls observe a successful refresh.
type orientationContext struct {
	flight   *tapper.Flight
	payload  string
	kegs     []tapper.OrientationKeg
	warnings []string
	recovery bool
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
		recovery := &orientationContext{payload: errMCPFlightRequired.Error(), recovery: true, warnings: []string{err.Error()}}
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
		recovery: candidate.Flight == nil,
	}
	g.publish(sessionID, next)
	return next, nil
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

func (g *sessionFlightGate) recoveryOnly(sessionID string) bool {
	current := g.current(sessionID)
	return current == nil || current.recovery
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

func (g *sessionFlightGate) authorizeMutation(sessionID string) error {
	current := g.current(sessionID)
	if current == nil || current.flight == nil {
		return errMCPFlightRequired
	}
	if !current.flight.HasCapability(tapper.FlightCapabilityManageFlights) {
		return errors.New("active flight does not grant manage_flights")
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
			payload: errMCPFlightRequired.Error(), warnings: []string{warning}, recovery: true,
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
		recovery: false,
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
	payload, payloadErr := tapper.BuildOrientationPayload(nil, "", nil, nil)
	if payloadErr != nil {
		payload = errMCPFlightRequired.Error()
	}
	g.publish(sessionIDFromContext(ctx), &orientationContext{payload: payload, recovery: true})
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
			if g.recoveryOnly(sessionID) {
				for _, tool := range listed.Tools {
					if recoveryToolNames[tool.Name] {
						copyResult.Tools = append(copyResult.Tools, tool)
					}
				}
				return &copyResult, nil
			}
			canManage := g.canManage(sessionID)
			for _, tool := range listed.Tools {
				if isFlightMutationTool(tool.Name) && !canManage {
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
			if params != nil && g.recoveryOnly(sessionID) && !recoveryToolNames[params.Name] {
				return errorResult(errMCPFlightRequired), nil
			}
			if params != nil && isFlightMutationTool(params.Name) {
				if err := g.authorizeMutation(sessionID); err != nil {
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
			if g.recoveryOnly(sessionID) {
				return nil, errMCPFlightRequired
			}
		}
		return next(ctx, method, req)
	}
}

func isFlightMutationTool(name string) bool {
	return name == "flight_create" || name == "flight_edit" || name == "flight_delete"
}
