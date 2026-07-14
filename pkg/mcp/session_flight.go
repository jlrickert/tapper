package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

const flightSwitchControlTool = "flight_switch_control"

var errMCPFlightRequired = errors.New("no flight is selected; KEG tools are locked. Inspect flights through MCP with `list_flights` and `flight_show`, ask the user to select a flight in Tapper configuration, then reconnect")

var recoveryToolNames = map[string]bool{
	"orient":       true,
	"list_flights": true,
	"flight_show":  true,
	"auth_status":  true,
	"config":       true,
}

type flightSessionContextKey struct{}

type flightSnapshot struct {
	Ref          string
	Source       string
	ManifestHash string
}

type flightSessionState struct {
	flight   *tapper.Flight
	snapshot flightSnapshot
	invalid  error
}

type sessionFlightGate struct {
	tap     *tapper.Tap
	initial string

	mu     sync.Mutex
	states map[string]*flightSessionState
}

func newSessionFlightGate(tap *tapper.Tap, initial string) *sessionFlightGate {
	return &sessionFlightGate{tap: tap, initial: strings.TrimSpace(initial), states: map[string]*flightSessionState{}}
}

// ValidateFullSurfaceFlight resolves the configured project/explicit flight
// before the stdio server starts. The returned canonical ref is safe to pin as
// the initial value for every new MCP session.
func ValidateFullSurfaceFlight(ctx context.Context, tap *tapper.Tap, explicit string) (string, error) {
	if tap == nil || tap.FlightService == nil {
		return "", errors.New("Tapper flight service is unavailable")
	}
	ref := tap.ActiveFlightName(explicit)
	if ref == "" {
		return "", nil
	}
	flight, err := tap.FlightService.GetFlightFresh(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("load active MCP flight %q: %w", ref, err)
	}
	if _, err := snapshotFlight(flight); err != nil {
		return "", err
	}
	tap.FlightService.StoreSessionFlight(ref, flight)
	return flight.Name, nil
}

func snapshotFlight(flight *tapper.Flight) (flightSnapshot, error) {
	if flight == nil || strings.TrimSpace(flight.Name) == "" {
		return flightSnapshot{}, errors.New("active MCP flight is unavailable")
	}
	snapshot := flightSnapshot{Ref: flight.Name, Source: flight.Source, ManifestHash: flight.ManifestHash}
	if snapshot.ManifestHash == "" {
		return flightSnapshot{}, errors.New("active MCP flight has no manifest hash")
	}
	return snapshot, nil
}

func (g *sessionFlightGate) state(ctx context.Context, sessionID string) *flightSessionState {
	if sessionID == "" {
		sessionID = "default"
	}
	g.mu.Lock()
	if state := g.states[sessionID]; state != nil {
		g.mu.Unlock()
		return state
	}
	g.mu.Unlock()

	state := &flightSessionState{}
	if g == nil || g.tap == nil || g.tap.FlightService == nil {
		state.invalid = errors.New("Tapper flight service is unavailable")
	} else if g.initial != "" {
		flight, err := g.tap.FlightService.GetFlightFresh(ctx, g.initial)
		if err != nil {
			state.invalid = fmt.Errorf("load active MCP flight %q: %w", g.initial, err)
		} else if snapshot, err := snapshotFlight(flight); err != nil {
			state.invalid = err
		} else {
			state.flight = flight
			state.snapshot = snapshot
			g.tap.FlightService.StoreSessionFlight(g.initial, flight)
		}
	}

	g.mu.Lock()
	if existing := g.states[sessionID]; existing != nil {
		g.mu.Unlock()
		return existing
	}
	g.states[sessionID] = state
	g.mu.Unlock()
	return state
}

func (g *sessionFlightGate) validate(ctx context.Context, sessionID string) (*tapper.Flight, error) {
	state := g.state(ctx, sessionID)
	g.mu.Lock()
	if state.invalid != nil {
		err := state.invalid
		g.mu.Unlock()
		return nil, err
	}
	if state.flight == nil {
		g.mu.Unlock()
		return nil, errMCPFlightRequired
	}
	snapshot := state.snapshot
	g.mu.Unlock()

	fresh, err := g.tap.FlightService.GetFlightFresh(ctx, snapshot.Ref)
	if err == nil {
		var freshSnapshot flightSnapshot
		freshSnapshot, err = snapshotFlight(fresh)
		if err == nil && freshSnapshot != snapshot {
			err = fmt.Errorf("active flight %s changed after this MCP session connected", snapshot.Ref)
		}
	}
	if err != nil {
		invalid := fmt.Errorf("MCP flight session invalidated: %w; use the human flight switch control or reconnect", err)
		g.mu.Lock()
		state.invalid = invalid
		g.mu.Unlock()
		return nil, invalid
	}
	return fresh, nil
}

func (g *sessionFlightGate) recoveryOnly(ctx context.Context, sessionID string) bool {
	state := g.state(ctx, sessionID)
	g.mu.Lock()
	defer g.mu.Unlock()
	return state.flight == nil && state.invalid == nil
}

func (g *sessionFlightGate) activeFlight(ctx context.Context) string {
	sessionID, _ := ctx.Value(flightSessionContextKey{}).(string)
	state := g.state(ctx, sessionID)
	g.mu.Lock()
	defer g.mu.Unlock()
	if state.flight == nil {
		return ""
	}
	return state.flight.Name
}

func (g *sessionFlightGate) canManage(ctx context.Context, sessionID string) bool {
	flight, err := g.validate(ctx, sessionID)
	return err == nil && flight.HasCapability(tapper.FlightCapabilityManageFlights)
}

func (g *sessionFlightGate) authorizeMutation(ctx context.Context, sessionID, target string, activeImmutable bool) error {
	flight, err := g.validate(ctx, sessionID)
	if err != nil {
		return err
	}
	if !flight.HasCapability(tapper.FlightCapabilityManageFlights) {
		return errors.New("active flight does not grant manage_flights")
	}
	if activeImmutable {
		ref, err := tapper.ParseFlightRef(target, flight.Namespace)
		if err != nil {
			return err
		}
		if ref.Canonical() == flight.Name {
			return errors.New("an MCP session cannot edit or delete its own active flight")
		}
	}
	return nil
}

func (g *sessionFlightGate) switchFlight(ctx context.Context, sessionID, target string) (*tapper.Flight, error) {
	if strings.TrimSpace(target) == "" {
		return nil, errors.New("flight reference is required")
	}
	flight, err := g.tap.FlightService.GetFlightFresh(ctx, target)
	if err != nil {
		return nil, err
	}
	snapshot, err := snapshotFlight(flight)
	if err != nil {
		return nil, err
	}
	state := &flightSessionState{flight: flight, snapshot: snapshot}
	g.mu.Lock()
	g.states[sessionID] = state
	g.mu.Unlock()
	g.tap.FlightService.StoreSessionFlight(target, flight)
	g.tap.FlightService.StoreSessionFlight(flight.Name, flight)
	return flight, nil
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
	// Stdio and the SDK's in-memory transport have no protocol-level session
	// ID. The ServerSession object is still stable and unique per connection.
	return fmt.Sprintf("session:%p", session)
}

func sessionIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(flightSessionContextKey{}).(string)
	if id == "" {
		return "default"
	}
	return id
}

func (g *sessionFlightGate) middleware(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		sessionID := sessionIDFromRequest(req)
		ctx = context.WithValue(ctx, flightSessionContextKey{}, sessionID)
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
			if g.recoveryOnly(ctx, sessionID) {
				for _, tool := range listed.Tools {
					if recoveryToolNames[tool.Name] {
						copyResult.Tools = append(copyResult.Tools, tool)
					}
				}
				return &copyResult, nil
			}
			canManage := g.canManage(ctx, sessionID)
			for _, tool := range listed.Tools {
				if tool.Name == flightSwitchControlTool {
					continue
				}
				if isFlightMutationTool(tool.Name) && !canManage {
					continue
				}
				copyResult.Tools = append(copyResult.Tools, tool)
			}
			return &copyResult, nil
		}
		if method == "tools/call" {
			params, _ := req.GetParams().(*sdkmcp.CallToolParamsRaw)
			if params != nil && g.recoveryOnly(ctx, sessionID) {
				switch {
				case params.Name == flightSwitchControlTool:
					// The hidden human control can unlock this connection.
				case params.Name == "orient":
					return errorResult(errMCPFlightRequired), nil
				case recoveryToolNames[params.Name]:
					return next(ctx, method, req)
				default:
					return errorResult(errMCPFlightRequired), nil
				}
			} else if params != nil && isFlightMutationTool(params.Name) {
				if err := g.authorizeMutation(ctx, sessionID, "", false); err != nil {
					return errorResult(err), nil
				}
			} else if params != nil && !isUngatedFlightTool(params.Name) {
				if _, err := g.validate(ctx, sessionID); err != nil {
					return errorResult(err), nil
				}
			}
		}
		if method == "resources/read" || method == "resources/subscribe" {
			if _, err := g.validate(ctx, sessionID); err != nil {
				return nil, err
			}
		}
		return next(ctx, method, req)
	}
}

func isFlightMutationTool(name string) bool {
	return name == "flight_create" || name == "flight_edit" || name == "flight_delete"
}

func isUngatedFlightTool(name string) bool {
	switch name {
	case flightSwitchControlTool, "list_flights", "flight_show", "auth_status", "config", "license":
		return true
	default:
		return false
	}
}

type flightSwitchControlInput struct {
	Ref string `json:"ref" jsonschema:"flight reference to activate for this MCP session"`
}

func registerFlightSwitchControl(srv *sdkmcp.Server, gate *sessionFlightGate) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        flightSwitchControlTool,
		Description: "Human-confirmed control for changing only this MCP session's active flight",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in flightSwitchControlInput) (*sdkmcp.CallToolResult, any, error) {
		candidate, err := gate.tap.FlightService.GetFlightFresh(ctx, in.Ref)
		if err != nil {
			return flightSwitchDecision("flight switch failed: " + err.Error()), nil, nil
		}
		result, err := req.Session.Elicit(ctx, &sdkmcp.ElicitParams{
			Message: fmt.Sprintf("Switch this Tapper MCP session to %s (%s)? This changes no project or user configuration.", candidate.Name, candidate.Title),
			RequestedSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"confirm": map[string]any{"type": "boolean", "title": "Confirm flight switch", "default": false},
				},
				"required": []string{"confirm"},
			},
		})
		if err != nil {
			return flightSwitchDecision("flight switch confirmation failed: " + err.Error()), nil, nil
		}
		confirmed, _ := result.Content["confirm"].(bool)
		if result.Action != "accept" || !confirmed {
			return flightSwitchDecision("flight switch was not confirmed"), nil, nil
		}
		flight, err := gate.switchFlight(ctx, sessionIDFromContext(ctx), candidate.Name)
		if err != nil {
			return flightSwitchDecision("flight switch failed: " + err.Error()), nil, nil
		}
		// Replacing and immediately removing a hidden marker uses the SDK's
		// debounced list-change notifier without changing the final tool set.
		type markerInput struct{}
		sdkmcp.AddTool(srv, &sdkmcp.Tool{Name: "_flight_transition_marker"}, func(context.Context, *sdkmcp.CallToolRequest, markerInput) (*sdkmcp.CallToolResult, any, error) {
			return textResult(""), nil, nil
		})
		srv.RemoveTools("_flight_transition_marker")
		return flightSwitchDecision("active flight: " + flight.Name), nil, nil
	})
}

// flightSwitchDecision is valid UserPromptExpansion hook output. Blocking the
// command expansion after the server-owned control finishes keeps the switch
// entirely outside the model turn while still surfacing the result to the user.
func flightSwitchDecision(reason string) *sdkmcp.CallToolResult {
	body, err := json.Marshal(map[string]string{"decision": "block", "reason": reason})
	if err != nil {
		return textResult(`{"decision":"block","reason":"flight switch result could not be encoded"}`)
	}
	return textResult(string(body))
}
