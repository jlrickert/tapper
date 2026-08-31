package keg_test

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestParseRemoteTargets(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		raw       string
		scheme    string
		canonical string
		host      string
		path      string
	}{
		{name: "https", raw: "https://hub.example.com/api/v1/@team/kegs/docs", scheme: kegpkg.SchemeHTTPs, canonical: "https://hub.example.com/api/v1/@team/kegs/docs", host: "hub.example.com", path: "/api/v1/@team/kegs/docs"},
		{name: "http", raw: "http://localhost:8080/api/v1/@team/kegs/docs", scheme: kegpkg.SchemeHTTP, canonical: "http://localhost:8080/api/v1/@team/kegs/docs", host: "localhost", path: "/api/v1/@team/kegs/docs"},
		{name: "implicit https", raw: "hub.example.com/api/v1/@team/kegs/docs", scheme: kegpkg.SchemeHTTPs, canonical: "https://hub.example.com/api/v1/@team/kegs/docs", host: "hub.example.com", path: "/api/v1/@team/kegs/docs"},
		{name: "keg reference", raw: "keg:@team/docs", scheme: kegpkg.SchemeAlias, canonical: "keg:@team/docs", path: "@team/docs"},
		{name: "unqualified keg", raw: "keg:docs", scheme: kegpkg.SchemeAlias, canonical: "keg:docs", path: "docs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			target, err := kegpkg.Parse(tc.raw)
			require.NoError(t, err)
			require.Equal(t, tc.scheme, target.Scheme())
			require.Equal(t, tc.canonical, target.String())
			require.Equal(t, tc.host, target.Host())
			require.Equal(t, filepath.FromSlash(tc.path), target.Path())
		})
	}
}

func TestParseRejectsFilesystemAndUnsupportedTargets(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		"/var/lib/kegs/docs",
		"./docs",
		"../docs",
		"~/kegs/docs",
		"C:\\kegs\\docs",
		"file:///var/lib/kegs/docs",
		"git://example.com/team/docs",
		"ssh://example.com/team/docs",
		"s3://bucket/docs",
		"team/docs",
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			_, err := kegpkg.Parse(raw)
			require.ErrorIs(t, err, kegpkg.ErrNotSupported)
		})
	}
}

func TestTargetYAMLRemoteOnly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		raw       string
		wantErr   error
		scheme    string
		hub       string
		namespace string
		kegName   string
		token     string
	}{
		{name: "url scalar", raw: "https://hub.example.com/api/v1/@team/kegs/docs\n", scheme: kegpkg.SchemeHTTPs},
		{name: "url mapping", raw: "url: https://hub.example.com/api/v1/@team/kegs/docs\ntoken: secret\n", scheme: kegpkg.SchemeHTTPs, token: "secret"},
		{name: "keg scalar", raw: "keg:@team/docs\n", scheme: kegpkg.SchemeAlias, namespace: "team", kegName: "docs"},
		{name: "structured keg", raw: "hub: enterprise\nnamespace: team\nkegName: docs\n", scheme: kegpkg.SchemeAlias, hub: "enterprise", namespace: "team", kegName: "docs"},
		{name: "path scalar", raw: "/var/lib/kegs/docs\n", wantErr: kegpkg.ErrNotSupported},
		{name: "file uri", raw: "file:///var/lib/kegs/docs\n", wantErr: kegpkg.ErrNotSupported},
		{name: "file mapping", raw: "file: /var/lib/kegs/docs\n", wantErr: kegpkg.ErrNotSupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var target kegpkg.Target
			err := yaml.Unmarshal([]byte(tc.raw), &target)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.scheme, target.Scheme())
			require.Equal(t, tc.hub, target.Hub)
			require.Equal(t, tc.namespace, target.Namespace)
			require.Equal(t, tc.kegName, target.KegName)
			require.Equal(t, tc.token, target.Token)
		})
	}
}

func TestTargetExpandExpandsRemoteFields(t *testing.T) {
	t.Parallel()

	env := toolkit.NewTestEnv(t.TempDir(), "/home/tester", "tester")
	require.NoError(t, env.Set("KEG_NAME", "blog"))
	require.NoError(t, env.Set("HUB_NAME", "enterprise"))
	require.NoError(t, env.Set("SECRET_TOKEN", "secret-token"))
	require.NoError(t, env.Set("TOKEN_ENV_KEY", "TAPPER_TOKEN"))

	target := kegpkg.Target{
		Url:      "https://example.com/${USER}/${KEG_NAME}",
		Hub:      "${HUB_NAME}",
		Password: "${SECRET_TOKEN}",
		Token:    "${SECRET_TOKEN}",
		TokenEnv: "${TOKEN_ENV_KEY}",
	}
	require.NoError(t, target.Expand(env))
	require.Equal(t, "https://example.com/tester/blog", target.Url)
	require.Equal(t, "enterprise", target.Hub)
	require.Equal(t, "secret-token", target.Password)
	require.Equal(t, "secret-token", target.Token)
	require.Equal(t, "TAPPER_TOKEN", target.TokenEnv)
}

func TestNewKegFromTargetRejectsUnresolvedAndUnsupportedTargets(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)

	_, err := kegpkg.NewKegFromTarget(fx.Context(), kegpkg.Target{KegName: "docs", Namespace: "team"}, fx.Runtime())
	require.Error(t, err)
	require.Contains(t, err.Error(), "no resolved hub url")

	_, err = kegpkg.NewKegFromTarget(fx.Context(), kegpkg.Target{}, fx.Runtime())
	require.ErrorIs(t, err, kegpkg.ErrNotSupported)
	require.True(t, errors.Is(err, kegpkg.ErrNotSupported))
}
