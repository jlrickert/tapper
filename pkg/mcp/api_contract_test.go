package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jlrickert/tapper/pkg/apicontract"
	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestMCPRESTCompatibilityInitializationAndDeployment(t *testing.T) {
	for _, initialMismatch := range []bool{true, false} {
		t.Run(map[bool]string{true: "initialization", false: "deployment"}[initialMismatch], func(t *testing.T) {
			var mismatch atomic.Bool
			mismatch.Store(initialMismatch)
			var discoveries, operations atomic.Int32
			hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/version" {
					discoveries.Add(1)
					require.Empty(t, r.Header.Get("Authorization"))
					versions := []string{apicontract.Revision}
					if mismatch.Load() {
						versions = []string{"2099-01-01"}
					}
					json.NewEncoder(w).Encode(apicontract.Discovery{ServerVersion: "hub-build", APIVersions: versions})
					return
				}
				operations.Add(1)
				require.Equal(t, apicontract.Revision, r.Header.Get(apicontract.VersionHeader))
				require.Equal(t, "client-build", r.Header.Get(apicontract.ClientHeader))
				if mismatch.Load() {
					w.WriteHeader(400)
					json.NewEncoder(w).Encode(apicontract.CompatibilityError{Code: apicontract.Unsupported, Message: "upgrade together", Requested: []string{apicontract.Revision}, Supported: []string{"2099-01-01"}, ServerVersion: "hub-build"})
					return
				}
				if r.URL.Path == "/api/v1/whoami" {
					json.NewEncoder(w).Encode(tapper.WhoAmI{UserID: 1, Username: "tester", DefaultNamespace: "tester"})
					return
				}
				json.NewEncoder(w).Encode([]any{})
			}))
			defer hub.Close()
			sb := newTestSandbox(t)
			rt := sb.Runtime()
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			require.NoError(t, rt.SetLogger(logger))
			configPath := "/home/testuser/contract.yaml"
			require.NoError(t, rt.AtomicWriteFile(configPath, []byte("hub: test\ndisableAtlasHub: true\nhubs:\n  test:\n    url: "+hub.URL+"\n    token: test-token\n"), 0600))
			tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt, ConfigPath: configPath})
			require.NoError(t, err)
			srv := mcp.NewServer(tap, "client-build", mcp.KegDefaults{}, mcp.ServerOptions{Logger: logger})
			ctx := context.Background()
			session := connectFlightSession(t, ctx, srv, nil)
			if initialMismatch {
				require.Zero(t, operations.Load())
				require.Contains(t, logs.String(), `"level":"ERROR"`)
			}
			mismatch.Store(true)
			result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "orient", Arguments: map[string]any{}})
			require.NoError(t, err)
			require.True(t, result.IsError)
			raw, err := json.Marshal(result.StructuredContent)
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, json.Unmarshal(raw, &payload))
			require.Equal(t, apicontract.Unsupported, payload["code"])
			require.Equal(t, false, payload["operationPerformed"])
			require.Equal(t, []any{"2099-01-01"}, payload["supported_api_versions"])
			require.EqualValues(t, 1, discoveries.Load())
			require.Contains(t, logs.String(), `"client_version":"client-build"`)
			require.NotContains(t, logs.String(), "test-token")
			if initialMismatch {
				listed, err := session.ListTools(ctx, nil)
				require.NoError(t, err)
				var names []string
				for _, tool := range listed.Tools {
					names = append(names, tool.Name)
				}
				require.Contains(t, names, "auth_info")
				require.Contains(t, names, "guide")
				require.NotContains(t, names, "create")
				require.True(t, strings.Contains(session.InitializeResult().Instructions, apicontract.Unsupported))
			}
		})
	}
}
