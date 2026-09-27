package cli

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUserAgentTransport(t *testing.T) {
	t.Parallel()
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("User-Agent"))
	}))
	defer srv.Close()
	client := &http.Client{Transport: userAgentTransport{base: http.DefaultTransport}}

	_, err := client.Get(srv.URL)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	req.Header.Set("User-Agent", "custom/1")
	_, err = client.Do(req)
	require.NoError(t, err)

	require.Equal(t, []string{"tap/" + Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")", "custom/1"}, got,
		"tap's agent is the default; a request's own agent wins")
}
