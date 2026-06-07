package keg_test

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/toolkit"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Tests for parsing and YAML unmarshalling of kegpkg.Target values.
// The table driven tests cover file paths, file URIs, tilde expansion,
// relative paths, shorthand hub:@user/keg form, and HTTP/HTTPS URLs.
func TestParse_File_TableDriven(t *testing.T) {
	// Use OS-specific temp dir so tests work across platforms.
	tmpDir := os.TempDir()
	absTmpKeg := filepath.Join(tmpDir, "keg")
	// Use a file URI that uses forward slashes as URLs expect.
	fileURI := "file://" + filepath.ToSlash(absTmpKeg)

	cases := []struct {
		name       string
		raw        string
		expand     bool // run kt.Expand(env) before assertions
		wantErr    bool
		wantSchema string
		wantFile   string
	}{
		{
			name:       "absolute path",
			raw:        absTmpKeg,
			wantSchema: kegpkg.SchemeFile,
			wantFile:   absTmpKeg,
		},
		{
			name:       "file uri",
			raw:        fileURI,
			wantSchema: kegpkg.SchemeFile,
			wantFile:   absTmpKeg,
		},
		{
			name:       "tilde path expands to home",
			raw:        "~/kegs/work",
			expand:     true,
			wantSchema: kegpkg.SchemeFile,
			wantFile:   "~/kegs/work",
		},
		{
			name:       "relative path",
			raw:        "kegs/work",
			wantSchema: kegpkg.SchemeFile,
			wantFile:   "kegs/work",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kt, err := kegpkg.Parse(tc.raw)
			require.NoError(t, err)
			if tc.expand {
				err = kt.Expand(&toolkit.OsEnv{})
				require.NoError(t, err)
				f, _ := toolkit.ExpandPath(&toolkit.OsEnv{}, tc.wantFile)
				tc.wantFile = f
			}
			if tc.wantSchema != "" {
				require.Equal(t, tc.wantSchema, kt.Scheme())
			}
			if tc.wantFile != "" {
				require.Equal(t, tc.wantFile, kt.File)
				require.Equal(t, tc.wantFile, kt.Path())
			}
		})
	}
}

// Table driven tests for YAML unmarshalling behavior.
// These ensure both scalar and mapping forms decode to the expected Target.
func TestUnmarshalYAML_TableDriven(t *testing.T) {
	cases := []struct {
		name          string
		rawYAML       []byte
		wantErr       bool
		wantSchema    string
		wantHost      string
		wantPath      string
		wantToken     string
		wantHub       string
		wantNamespace string
		wantKegName   string
		wantFile      string
		wantUrl       string
	}{
		{
			name:       "https: simple url mapping",
			rawYAML:    []byte("url: example.com/owner/repo"),
			wantSchema: kegpkg.SchemeHTTPs,
			wantHost:   "example.com",
			wantPath:   "/owner/repo",
			wantUrl:    "https://example.com/owner/repo",
		},
		{
			name:       "https: simple url scalar",
			rawYAML:    []byte("example.com/owner/repo"),
			wantSchema: kegpkg.SchemeHTTPs,
			wantHost:   "example.com",
			wantPath:   "/owner/repo",
			wantUrl:    "https://example.com/owner/repo",
		},
		{
			name: "https: url + token mapping",
			// Use raw string literal for readability and to avoid long line joins.
			rawYAML: []byte(`url: https://keg.example.com/@user/keg
token: secret123
`),
			wantSchema: kegpkg.SchemeHTTPs,
			wantUrl:    "https://keg.example.com/@user/keg",
			wantHost:   "keg.example.com",
			wantPath:   "/@user/keg",
			wantToken:  "secret123",
		},
		{
			name:          "api: structured hub+namespace+kegName mapping pins the hub",
			rawYAML:       []byte("hub: jlr\nnamespace: jlrickert\nkegName: tapper\n"),
			wantSchema:    kegpkg.SchemeAlias,
			wantHub:       "jlr",
			wantNamespace: "jlrickert",
			wantKegName:   "tapper",
		},
		{
			name:          "api: canonical keg scalar (hub resolved from namespace)",
			rawYAML:       []byte("keg:@jlrickert/tapper"),
			wantSchema:    kegpkg.SchemeAlias,
			wantHub:       "",
			wantNamespace: "jlrickert",
			wantKegName:   "tapper",
		},
		{
			name:       "file: simple path",
			rawYAML:    []byte("/home/testuser/kegs/public"),
			wantSchema: kegpkg.SchemeFile,
			wantFile:   "/home/testuser/kegs/public",
		},
		{
			name:       "file: with home expansion",
			rawYAML:    []byte("~/kegs/public"),
			wantSchema: kegpkg.SchemeFile,
			wantFile:   "~/kegs/public",
		},
		{
			name:       "file: relative path",
			rawYAML:    []byte("../../kegs/public"),
			wantSchema: kegpkg.SchemeFile,
			wantFile:   "../../kegs/public",
		},
		{
			name:       "file: screwy relative path",
			rawYAML:    []byte("..//../kegs/public"),
			wantSchema: kegpkg.SchemeFile,
			wantFile:   "../../kegs/public",
		},
		{
			name:       "file: with explicit scheme",
			rawYAML:    []byte("file:///home/testuser/kegs/public"),
			wantSchema: kegpkg.SchemeFile,
			wantFile:   "/home/testuser/kegs/public",
		},
		{
			name:       "file: path w/ explicit scheme and home",
			rawYAML:    []byte("file://~/kegs/public"),
			wantSchema: kegpkg.SchemeFile,
			wantFile:   "~/kegs/public",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var kt kegpkg.Target
			err := yaml.Unmarshal(tc.rawYAML, &kt)
			if tc.wantErr {
				require.Error(t, err, tc.name)
				return
			}
			require.NoError(t, err)
			if tc.wantSchema != "" {
				require.Equal(t, tc.wantSchema, kt.Scheme())
			}
			if tc.wantFile != "" {
				// Normalize the expected path to the current OS style before compare.
				exp := filepath.FromSlash(tc.wantFile)
				require.Equal(t, exp, kt.File)
			}
			if tc.wantHub != "" {
				require.Equal(t, tc.wantHub, kt.Hub)
			}
			if tc.wantUrl != "" {
				require.Equal(t, tc.wantUrl, kt.Url)
			}
			if tc.wantHost != "" {
				require.Equal(t, tc.wantHost, kt.Host())
			}
			if tc.wantPath != "" {
				require.Equal(t, tc.wantPath, kt.Path())
			}
			if tc.wantToken != "" {
				require.Equal(t, tc.wantToken, kt.Token)
			}
			if tc.wantNamespace != "" {
				require.Equal(t, tc.wantNamespace, kt.Namespace)
			}
			if tc.wantKegName != "" {
				require.Equal(t, tc.wantKegName, kt.KegName)
			}
			// Ensure the String result is parseable as a URL when non-empty.
			if kt.String() != "" {
				_, err := url.Parse(kt.String())
				require.NoError(t, err, tc.name)
			}
		})
	}
}

func TestTargetExpand_ExpandsEnvironmentVariables(t *testing.T) {
	t.Parallel()

	jail := t.TempDir()
	home := filepath.Join(string(filepath.Separator), "home", "tester")
	env := toolkit.NewTestEnv(jail, home, "tester")
	require.NoError(t, env.Set("KEG_NAME", "blog"))
	require.NoError(t, env.Set("HUB_NAME", "knut"))
	require.NoError(t, env.Set("SECRET_TOKEN", "secret-token"))
	require.NoError(t, env.Set("TOKEN_ENV_KEY", "TAPPER_TOKEN"))

	kt := kegpkg.Target{
		File:     "~/${KEG_NAME}/keg",
		Url:      "https://example.com/${USER}/${KEG_NAME}",
		Hub:      "${HUB_NAME}",
		Password: "${SECRET_TOKEN}",
		Token:    "${SECRET_TOKEN}",
		TokenEnv: "${TOKEN_ENV_KEY}",
	}

	err := kt.Expand(env)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, "blog", "keg"), kt.File)
	require.Equal(t, "https://example.com/tester/blog", kt.Url)
	require.Equal(t, "knut", kt.Hub)
	require.Equal(t, "secret-token", kt.Password)
	require.Equal(t, "secret-token", kt.Token)
	require.Equal(t, "TAPPER_TOKEN", kt.TokenEnv)
}

// TestParse_KegScheme_Canonicalization pins that the accepted input variants of
// the "keg:" scheme parse to the same hub-agnostic Target and round-trip
// through String() as the canonical "keg:@namespace/keg" form. The "@" sigil is
// stripped on parse so the stored namespace never carries it; Path() and
// String() re-apply it. The hub is resolved from the namespace, never encoded.
func TestParse_KegScheme_Canonicalization(t *testing.T) {
	t.Parallel()

	variants := []string{
		"keg:@jlrickert/tapper",
		"keg:/@jlrickert/tapper",
	}

	for _, raw := range variants {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			kt, err := kegpkg.Parse(raw)
			require.NoError(t, err)
			require.Equal(t, kegpkg.SchemeAlias, kt.Scheme())
			require.Equal(t, "", kt.Hub, "the keg scheme never pins a hub")
			require.Equal(t, "jlrickert", kt.Namespace, "@ sigil must be stripped on parse")
			require.Equal(t, "tapper", kt.KegName)
			require.Equal(t, "keg:@jlrickert/tapper", kt.String(), "String() must emit the canonical keg scheme")
			require.Equal(t, filepath.Join("@jlrickert", "tapper"), kt.Path(), "Path() must re-apply @ exactly once")
		})
	}

	// A "<hub>:@ns/keg" form is not a keg reference — there is no such scheme.
	// It is not classified as the keg scheme (it falls through to file/url).
	t.Run("hub-prefixed form is not a keg reference", func(t *testing.T) {
		t.Parallel()
		kt, err := kegpkg.Parse("jlr:@jlrickert/tapper")
		require.NoError(t, err)
		require.NotEqual(t, kegpkg.SchemeAlias, kt.Scheme(), "a non-keg scheme prefix is not a keg reference")
	})
}
