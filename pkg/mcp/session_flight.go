package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

var errMCPFlightRequired = errors.New("the explicitly configured flight could not be activated; KEG tools are locked. Inspect flights with `list_flights` and `flight_show`, repair that exact selection outside MCP, then call `session_refresh` and `orient`")

// ErrOrientationStale is returned without performing the requested operation.
var ErrOrientationStale = fmt.Errorf("ORIENTATION_STALE: authority changed between per-call resolution and dispatch; retry the operation yourself after reviewing current authority. Mutations are never replayed automatically: %w", keg.ErrOrientationStale)

// ErrOrientationDenied reports a fresh orientation that lacks the requested
// authority. Selecting a different accessible flight is explicit per call.
var ErrOrientationDenied = fmt.Errorf("ORIENTATION_DENIED: the requested flight is not selectable from this connection's current authority or does not grant this operation. The operation was not performed: %w", keg.ErrOrientationDenied)

// ErrOrientationUnavailable reports a transient failure to recompute live
// authority. The caller may retry later, but the operation is never replayed.
var ErrOrientationUnavailable = fmt.Errorf("ORIENTATION_UNAVAILABLE: current orientation authority could not be verified; retry after the Hub is available. The operation was not performed: %w", keg.ErrOrientationUnavailable)

// ErrOrientationRootUnavailable reports permanent loss of the connection-pinned root.
// A different root requires a newly launched session.
var ErrOrientationRootUnavailable = fmt.Errorf("ORIENTATION_ROOT_UNAVAILABLE: the connection-pinned root was deleted or is no longer accessible; start a new session to choose a different root. The operation was not performed: %w", keg.ErrOrientationRootUnavailable)

// failedOrientationPayload describes a selection that was made but could not be
// resolved. It deliberately does not reuse errMCPFlightRequired: reporting "no
// flight is selected" when one was selected and merely failed to resolve sends
// the reader looking for missing configuration instead of the real fault, which
// is usually a wrong flight name or an unreachable hub.
func failedOrientationPayload(err error) string {
	return "This session could not establish flight authority: " + err.Error() +
		"\n\nKEG tools are locked until it does, so only `orient`, `session_refresh`, `list_flights`," +
		" `flight_show`, `auth_info`, and `keg_search` are published. An empty cover on a" +
		" successfully loaded flight would still publish the complete registered" +
		" inventory. Call `list_flights` to see what" +
		" actually exists, then ask the user to correct the selected flight in" +
		" Tapper configuration, then call `session_refresh`, then `orient` on this same connection." +
		" An empty flight list usually means this machine is not bootstrapped or" +
		" not authenticated to the hub that hosts the flight."
}

var recoveryToolNames = map[string]bool{
	"orient":          true,
	"session_refresh": true,
	"list_flights":    true,
	"flight_show":     true,
	"auth_info":       true,
	"keg_search":      true,
}

// ungovernedToolNames never select authority and therefore do not advertise a
// flight argument. Keep administration/configuration discovery here even when
// a particular build does not register those tools.
var ungovernedToolNames = map[string]bool{
	"auth_info": true, "auth_status": true, "keg_search": true,
	"session_refresh": true,
	"config":          true, "config_template": true,
	"namespace_list": true, "namespace_create": true, "namespace_members": true,
	"namespace_add_member": true, "namespace_set_role": true, "namespace_remove_member": true,
	"license": true, "list_flights": true, "flight_show": true,
}

// sessionMode is the authority state of one MCP session.
type sessionMode int

const (
	// modeActive: either no-flight identity authority or a real flight governs
	// the session.
	modeActive sessionMode = iota
	// modeSelect: an explicitly configured flight failed to activate. The agent
	// cannot change configuration itself, so only recovery tools are offered.
	modeSelect
)

const toolListTransitionMarker = "_session_tool_list_transition_marker"

// initializationInstructions carries the static KEG operating rules plus the
// directive to orient. The rules are here so a caller that has already been
// told which flight to pass — a subagent briefed by a coordinator, say — can
// work without spending a round trip on orientation it does not need. They do
// not carry session state and cannot go stale.
//
// The directive still matters: only orient reports the flight, its cover, and
// the available KEGs, and only orient survives a context reset, because these
// instructions are captured once at connection and are never re-sent.
func initializationInstructions() string {
	// The rules already say to orient first and to orient again after a context
	// reset; this adds only what they do not: what orient is for.
	return tapper.OrientationOperatingRules() +
		"\nCall `orient` for this session's current authority, instructions, and available KEGs.\n"
}

type flightSessionContextKey struct{}

// orientationContext is immutable after publication. Tool calls capture its
// pointer at their boundary, so an in-flight call finishes under the authority
// with which it began while later calls observe a successful refresh.
type orientationContext struct {
	root             *tapper.Flight
	flight           *tapper.Flight
	path             []string
	availableFlights []string
	identity         string
	revision         string
	payload          string
	kegs             []tapper.OrientationKeg
	aggregateKegs    []tapper.OrientationKeg
	warnings         []string
	fullAccess       bool
	reconnect        string
	mode             sessionMode
}

type flightSessionState struct {
	mu        sync.RWMutex
	refreshMu sync.Mutex
	current   *orientationContext
}

type sessionFlightGate struct {
	provider OrientationProvider

	mu     sync.Mutex
	states map[string]*flightSessionState
	srv    *sdkmcp.Server
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

// loadAndPin establishes no-flight authority or a real root for a new session,
// and retries the exact configured root for recovery. Once active, ordinary
// calls never publish a call-local selection into shared state.
func (g *sessionFlightGate) loadAndPin(ctx context.Context, sessionID string) (*orientationContext, error) {
	state := g.state(sessionID)
	state.refreshMu.Lock()
	defer state.refreshMu.Unlock()
	current := g.current(sessionID)
	if currentMode(current) == modeActive {
		return current, nil
	}
	candidate, err := g.provider.Load(ctx)
	if err != nil {
		if current != nil {
			return current, err
		}
		// Initialization must remain connectable for recovery.
		recovery := &orientationContext{payload: failedOrientationPayload(err), mode: modeSelect, warnings: []string{err.Error()}}
		state.mu.Lock()
		state.current = recovery
		state.mu.Unlock()
		return recovery, err
	}
	next, err := makeOrientationContext(candidate)
	if err != nil {
		return current, err
	}
	// Aggregate authority is intentionally call-local. The pinned session keeps
	// only root context for auth_info and future live resolutions.
	next.aggregateKegs = nil
	g.publish(sessionID, next, false)
	return next, nil
}

type sessionRefreshOutput struct {
	Status       string `json:"status"`
	Root         string `json:"root,omitempty"`
	ToolsChanged bool   `json:"toolsChanged"`
	NextAction   string `json:"nextAction,omitempty"`
}

func (g *sessionFlightGate) refresh(ctx context.Context, sessionID string) (sessionRefreshOutput, error) {
	state := g.state(sessionID)
	state.refreshMu.Lock()
	defer state.refreshMu.Unlock()

	current := g.current(sessionID)
	if currentMode(current) == modeActive {
		nextAction := "orient"
		if current.fullAccess {
			nextAction = "new_session"
		}
		root := ""
		if current.root != nil {
			root = current.root.Name
		}
		return sessionRefreshOutput{
			Status: "already_active", Root: root,
			ToolsChanged: false, NextAction: nextAction,
		}, nil
	}

	candidate, err := g.provider.Load(ctx)
	if err != nil {
		return sessionRefreshOutput{}, err
	}
	next, err := makeOrientationContext(candidate)
	if err != nil {
		return sessionRefreshOutput{}, err
	}
	if next.fullAccess {
		return sessionRefreshOutput{}, fmt.Errorf(
			"the failed configured flight cannot fall back to no-flight full access on this connection; start a new MCP connection",
		)
	}
	next.aggregateKegs = nil
	toolsChanged := toolSurfaceChanged(current, next)
	g.publish(sessionID, next, toolsChanged)

	switch currentMode(next) {
	case modeActive:
		return sessionRefreshOutput{
			Status: "activated", Root: next.root.Name,
			ToolsChanged: toolsChanged, NextAction: "orient",
		}, nil
	default:
		return sessionRefreshOutput{Status: "selection_required", ToolsChanged: toolsChanged}, nil
	}
}

// resolveCall computes live graph, identity, and selected authority for one
// invocation. It never mutates session state.
func (g *sessionFlightGate) resolveCall(ctx context.Context, sessionID, selected string) (*orientationContext, error) {
	pinned := g.current(sessionID)
	if currentMode(pinned) != modeActive || (!pinned.fullAccess && pinned.root == nil) {
		return nil, lockedError(pinned)
	}
	resolver, ok := g.provider.(FlightOrientationProvider)
	if !ok {
		return nil, fmt.Errorf("%w: per-call flight selection is unavailable for this orientation provider", ErrOrientationUnavailable)
	}
	rootRef := ""
	if pinned.root != nil {
		rootRef = pinned.root.Name
	}
	candidate, err := resolver.Resolve(ctx, rootRef, selected)
	if err != nil {
		return nil, err
	}
	return makeOrientationContext(candidate)
}

func makeOrientationContext(candidate *Orientation) (*orientationContext, error) {
	if candidate == nil {
		candidate = &Orientation{}
	}
	if candidate.Revision == "" {
		if err := FinalizeOrientation(candidate); err != nil {
			return nil, err
		}
	}
	next := &orientationContext{
		root: cloneFlight(candidate.Root), flight: cloneFlight(candidate.Flight),
		path:             append([]string(nil), candidate.Path...),
		availableFlights: append([]string(nil), candidate.AvailableFlights...),
		identity:         candidate.Identity, revision: candidate.Revision, payload: candidate.Payload,
		kegs:          append([]tapper.OrientationKeg(nil), candidate.Kegs...),
		aggregateKegs: append([]tapper.OrientationKeg(nil), candidate.AggregateKegs...),
		warnings:      append([]string(nil), candidate.Warnings...), fullAccess: candidate.FullAccess,
		reconnect: candidate.ReconnectInstructions, mode: modeFor(candidate),
	}
	if next.root == nil {
		next.root = cloneFlight(candidate.Flight)
	}
	return next, nil
}

// modeFor classifies a loaded candidate. A no-flight full-access candidate is
// active even though it has no flight object; an ordinary nil flight is failed
// explicit-selection recovery.
func modeFor(orientation *Orientation) sessionMode {
	if orientation == nil || (!orientation.FullAccess && orientation.Flight == nil) {
		return modeSelect
	}
	return modeActive
}

func (g *sessionFlightGate) publish(sessionID string, next *orientationContext, notify bool) {
	state := g.state(sessionID)
	state.mu.Lock()
	state.current = next
	state.mu.Unlock()
	if notify {
		g.notifyToolsChanged()
	}
}

func cloneFlight(f *tapper.Flight) *tapper.Flight {
	if f == nil {
		return nil
	}
	out := *f
	out.Capabilities = append([]tapper.FlightCapability(nil), f.Capabilities...)
	out.Cover = append([]tapper.FlightCover(nil), f.Cover...)
	out.Subflights = append([]string(nil), f.Subflights...)
	out.AllowedKegs = append([]string(nil), f.AllowedKegs...)
	return &out
}

func (g *sessionFlightGate) notifyToolsChanged() {
	if g.srv == nil {
		return
	}
	type markerInput struct{}
	sdkmcp.AddTool(g.srv, &sdkmcp.Tool{Name: toolListTransitionMarker}, func(context.Context, *sdkmcp.CallToolRequest, markerInput) (*sdkmcp.CallToolResult, any, error) {
		return textResult(""), nil, nil
	})
}

func toolSurfaceChanged(current, next *orientationContext) bool {
	left, right := allowedTools(current), allowedTools(next)
	if left == nil || right == nil {
		return left != nil || right != nil
	}
	if len(left) != len(right) {
		return true
	}
	for name := range left {
		if !right[name] {
			return true
		}
	}
	return false
}

func currentMode(current *orientationContext) sessionMode {
	if current == nil {
		return modeSelect
	}
	return current.mode
}

// allowedTools returns the allowlist governing current, or nil when every
// registered tool is available.
func allowedTools(current *orientationContext) map[string]bool {
	switch currentMode(current) {
	case modeSelect:
		return recoveryToolNames
	default:
		return nil
	}
}

func lockedError(current *orientationContext) error {
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

func (g *sessionFlightGate) authorizeMutation(ctx context.Context) error {
	return g.authorizeCapability(orientationFromContext(ctx), tapper.FlightCapabilityManageFlights)
}

func (g *sessionFlightGate) authorizeKegCreation(ctx context.Context) error {
	return g.authorizeCapability(orientationFromContext(ctx), tapper.FlightCapabilityManageKegs)
}

func (g *sessionFlightGate) fullAccessReconnect(ctx context.Context) string {
	current := orientationFromContext(ctx)
	if current != nil && current.fullAccess {
		return current.reconnect
	}
	pinned := g.current(sessionIDFromContext(ctx))
	if pinned != nil && pinned.fullAccess {
		return pinned.reconnect
	}
	return ""
}

func (g *sessionFlightGate) authorizeCapability(current *orientationContext, capability tapper.FlightCapability) error {
	if current == nil || current.flight == nil {
		if current != nil && current.fullAccess {
			return nil
		}
		return errMCPFlightRequired
	}
	if !current.flight.HasCapability(capability) {
		return fmt.Errorf("%w: selected flight does not grant %s", ErrOrientationDenied, capability)
	}
	return nil
}

func (g *sessionFlightGate) orientationTarget(ctx context.Context, target string) (root, active bool, err error) {
	current := orientationFromContext(ctx)
	pinned := g.current(sessionIDFromContext(ctx))
	if (current != nil && current.fullAccess) || (pinned != nil && pinned.fullAccess) {
		return false, false, nil
	}
	if current == nil || current.flight == nil {
		return false, false, errMCPFlightRequired
	}
	ref, err := tapper.ParseFlightRef(target, current.flight.Namespace)
	if err != nil {
		return false, false, err
	}
	canonical := ref.Canonical()
	return current.root != nil && canonical == current.root.Name, canonical == current.flight.Name, nil
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

func contextWithOrientation(ctx context.Context, current *orientationContext) context.Context {
	if current == nil || current.fullAccess || current.root == nil || current.flight == nil || current.revision == "" {
		return ctx
	}
	return keg.WithOrientationState(ctx, keg.OrientationState{
		Root: current.root.Name, Active: current.flight.Name, Revision: current.revision,
	})
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

// SessionFullAccess reports whether the current call runs under no-flight
// identity authority. Such a call has no flight snapshot but is not restricted:
// it reaches everything the identity reaches. Discovery tools must distinguish it
// from the other flightless state — failed-root recovery, which reaches nothing —
// because both report a nil SessionFlight.
func SessionFullAccess(ctx context.Context) bool {
	current := orientationFromContext(ctx)
	return current != nil && current.fullAccess
}

// SessionOrientationKegs returns the exact selected-flight projection when
// flight was supplied, or the live no-flight identity / pinned-root graph
// projection when discovery omitted it. Aggregate rows are call-local and
// never cached in session state.
func SessionOrientationKegs(ctx context.Context) []tapper.OrientationKeg {
	current := orientationFromContext(ctx)
	if current == nil {
		return nil
	}
	if graphDiscovery, _ := ctx.Value(graphDiscoveryContextKey{}).(bool); graphDiscovery {
		return append([]tapper.OrientationKeg(nil), current.aggregateKegs...)
	}
	return append([]tapper.OrientationKeg(nil), current.kegs...)
}

// HasSessionOrientation reports whether the current call is governed by a
// session orientation gate. A governed recovery session has no flight but
// still returns true; ungated embedded surfaces return false.
func HasSessionOrientation(ctx context.Context) bool {
	return orientationFromContext(ctx) != nil
}

type orientationContextKey struct{}
type graphDiscoveryContextKey struct{}

func (g *sessionFlightGate) middleware(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		sessionID := sessionIDFromRequest(req)
		ctx = context.WithValue(ctx, flightSessionContextKey{}, sessionID)

		if method == "initialize" {
			current, _ := g.loadAndPin(ctx, sessionID)
			callCtx := context.WithValue(ctx, orientationContextKey{}, current)
			result, err := next(contextWithOrientation(callCtx, current), method, req)
			if err != nil {
				return result, err
			}
			if initialized, ok := result.(*sdkmcp.InitializeResult); ok {
				copyResult := *initialized
				copyResult.Instructions = initializationInstructions()
				return &copyResult, nil
			}
			return result, nil
		}

		current := g.current(sessionID)
		ctx = context.WithValue(ctx, orientationContextKey{}, current)
		ctx = contextWithOrientation(ctx, current)
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
			allowed := allowedTools(current)
			for _, tool := range listed.Tools {
				if tool.Name == toolListTransitionMarker {
					continue
				}
				if allowed != nil && !allowed[tool.Name] {
					continue
				}
				copyTool := *tool
				if authorityBearingTool(tool.Name) {
					copyTool.InputSchema = schemaWithFlight(tool.InputSchema)
				}
				copyResult.Tools = append(copyResult.Tools, &copyTool)
			}
			return &copyResult, nil
		}
		if method == "tools/call" {
			params, _ := req.GetParams().(*sdkmcp.CallToolParamsRaw)
			if allowed := allowedTools(current); params != nil && allowed != nil && !allowed[params.Name] {
				return errorResult(lockedError(current)), nil
			}
			if params == nil || !authorityBearingTool(params.Name) {
				return next(ctx, method, req)
			}
			if params.Name == "keg_list" {
				present, validationErr := toolArgumentPresent(params, "all")
				if validationErr != nil {
					return errorResult(validationErr), nil
				}
				if present {
					return errorResult(errors.New(`validating "arguments": validating root: unexpected additional properties ["all"]`)), nil
				}
			}
			selected, err := extractFlightArgument(params)
			if err != nil {
				return orientationFailureResult(fmt.Errorf("%w: %v", ErrOrientationDenied, err)), nil
			}
			var callOrientation *orientationContext
			switch currentMode(current) {
			case modeActive:
				callOrientation, err = g.resolveCall(ctx, sessionID, selected)
			case modeSelect:
				if params.Name == "orient" {
					if selected != "" {
						return errorResult(errors.New("cannot select a flight before this connection has an active pinned root; call `session_refresh`, then `orient`")), nil
					}
					callOrientation = current
				} else {
					err = lockedError(current)
				}
			}
			if err != nil {
				return orientationFailureResult(err), nil
			}
			ctx = context.WithValue(ctx, orientationContextKey{}, callOrientation)
			ctx = context.WithValue(ctx, graphDiscoveryContextKey{}, params.Name == "keg_list" && selected == "")
			ctx = contextWithOrientation(ctx, callOrientation)
			if isFlightMutationTool(params.Name) {
				if err := g.authorizeMutation(ctx); err != nil {
					return orientationFailureResult(err), nil
				}
			}
			if isKegCreationTool(params.Name) {
				if err := g.authorizeKegCreation(ctx); err != nil {
					return orientationFailureResult(err), nil
				}
			}
			return next(ctx, method, req)
		}
		if method == "resources/read" || method == "resources/subscribe" {
			if params, ok := req.GetParams().(*sdkmcp.ReadResourceParams); ok && params.URI == orientResourceURI {
				if currentMode(current) == modeActive {
					resolved, err := g.resolveCall(ctx, sessionID, "")
					if err != nil {
						return nil, err
					}
					ctx = context.WithValue(ctx, orientationContextKey{}, resolved)
					ctx = contextWithOrientation(ctx, resolved)
				}
				return next(ctx, method, req)
			}
			if currentMode(current) != modeActive {
				return nil, lockedError(current)
			}
			resolved, err := g.resolveCall(ctx, sessionID, "")
			if err != nil {
				return nil, err
			}
			ctx = context.WithValue(ctx, orientationContextKey{}, resolved)
			ctx = contextWithOrientation(ctx, resolved)
		}
		return next(ctx, method, req)
	}
}

func authorityBearingTool(name string) bool {
	return name != "" && !ungovernedToolNames[name]
}

func toolArgumentPresent(params *sdkmcp.CallToolParamsRaw, name string) (bool, error) {
	if params == nil || len(params.Arguments) == 0 {
		return false, nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(params.Arguments, &object); err != nil {
		return false, fmt.Errorf("tool arguments must be an object: %w", err)
	}
	_, ok := object[name]
	return ok, nil
}

func extractFlightArgument(params *sdkmcp.CallToolParamsRaw) (string, error) {
	if params == nil || len(params.Arguments) == 0 {
		return "", nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(params.Arguments, &object); err != nil {
		return "", fmt.Errorf("tool arguments must be an object: %w", err)
	}
	raw, ok := object["flight"]
	if !ok {
		return "", nil
	}
	var selected string
	if err := json.Unmarshal(raw, &selected); err != nil {
		return "", errors.New("flight must be a string")
	}
	delete(object, "flight")
	clean, err := json.Marshal(object)
	if err != nil {
		return "", err
	}
	params.Arguments = clean
	return strings.TrimSpace(selected), nil
}

func schemaWithFlight(schema any) any {
	raw, err := json.Marshal(schema)
	if err != nil {
		return schema
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return schema
	}
	properties, _ := object["properties"].(map[string]any)
	if properties == nil {
		properties = map[string]any{}
		object["properties"] = properties
	}
	properties["flight"] = map[string]any{
		"type":        "string",
		"description": "optional real flight available to current connection authority; omitted uses the connection-pinned authority",
	}
	return object
}

func orientationFailureResult(err error) *sdkmcp.CallToolResult {
	if err == nil {
		err = ErrOrientationStale
	}
	code := "ORIENTATION_STALE"
	switch {
	case errors.Is(err, ErrOrientationRootUnavailable):
		code = "ORIENTATION_ROOT_UNAVAILABLE"
	case errors.Is(err, ErrOrientationUnavailable):
		code = "ORIENTATION_UNAVAILABLE"
	case errors.Is(err, ErrOrientationDenied):
		code = "ORIENTATION_DENIED"
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: err.Error()}},
		StructuredContent: map[string]any{
			"code":               code,
			"reorientRequired":   false,
			"operationPerformed": false,
		},
		IsError: true,
	}
}

func sessionRefreshFailureResult(current *orientationContext, err error) *sdkmcp.CallToolResult {
	if err == nil {
		err = errors.New("session refresh failed")
	}
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "SESSION_REFRESH_FAILED: " + err.Error()}},
		StructuredContent: map[string]any{
			"code":         "SESSION_REFRESH_FAILED",
			"mode":         sessionModeName(currentMode(current)),
			"toolsChanged": false,
		},
		IsError: true,
	}
}

func sessionModeName(mode sessionMode) string {
	switch mode {
	case modeActive:
		return "active"
	default:
		return "recovery"
	}
}

func isFlightMutationTool(name string) bool {
	return name == "flight_create" || name == "flight_edit" || name == "flight_delete"
}

func isKegCreationTool(name string) bool {
	return name == "keg_create"
}
