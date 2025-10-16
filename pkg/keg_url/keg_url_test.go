package kegurl_test

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"

	std "github.com/jlrickert/go-std/pkg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Tests for parsing and YAML unmarshalling of kegurl.Target values.
// The table driven tests cover file paths, file URIs, tilde expansion,
// relative paths, shorthand registry:user/keg form, and HTTP/HTTPS URLs.
func TestParse_File_TableDriven(t *testing.T) {
	jail := t.TempDir()
	// Prepare a test Env for tilde expansion checks.
	env := std.NewTestEnv(jail, filepath.FromSlash("/home/testuser"), "testuser")
	ctx := std.WithEnv(t.Context(), env)

	// Use OS-specific temp dir so tests work across platforms.
	tmpDir := os.TempDir()
	absTmpKeg := filepath.Join(tmpDir, "keg")
	// Use a file URI that uses forward slashes as URLs expect.
	fileURI := "file://" + filepath.ToSlash(absTmpKeg)

	cases := []struct {
		name       string
		raw        string
		expand     bool // run kt.Expand(ctx) before assertions
		wantErr    bool
		wantSchema string
		wantFile   string
	}{
		{
			name:       "absolute path",
			raw:        absTmpKeg,
			wantSchema: kegurl.SchemeFile,
			wantFile:   absTmpKeg,
		},
		{
			name:       "file uri",
			raw:        fileURI,
			wantSchema: kegurl.SchemeFile,
			wantFile:   absTmpKeg,
		},
		{
			name:       "tilde path expands to home",
			raw:        "~/kegs/work",
			expand:     true,
			wantSchema: kegurl.SchemeFile,
			wantFile:   "~/kegs/work",
		},
		{
			name:       "relative path",
			raw:        "kegs/work",
			wantSchema: kegurl.SchemeFile,
			wantFile:   "kegs/work",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			kt, err := kegurl.Parse(tc.raw)
			require.NoError(t, err)
			if tc.expand {
				err = kt.Expand(ctx)
				require.NoError(t, err)
				f, _ := std.ExpandPath(ctx, tc.wantFile)
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
		name       string
		rawYAML    []byte
		wantErr    bool
		wantSchema string
		wantHost   string
		wantPath   string
		wantToken  string
		wantRepo   string
		wantUser   string
		wantKeg    string
		wantFile   string
		wantUrl    string
	}{
		{
			name:       "https: simple url mapping",
			rawYAML:    []byte("url: example.com/owner/repo"),
			wantSchema: kegurl.SchemeHTTPs,
			wantHost:   "example.com",
			wantPath:   "/owner/repo",
			wantUrl:    "https://example.com/owner/repo",
		},
		{
			name:       "https: simple url scalar",
			rawYAML:    []byte("example.com/owner/repo"),
			wantSchema: kegurl.SchemeHTTPs,
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
			wantSchema: kegurl.SchemeHTTPs,
			wantUrl:    "https://keg.example.com/@user/keg",
			wantHost:   "keg.example.com",
			wantPath:   "/@user/keg",
			wantToken:  "secret123",
		},
		{
			name:       "api: structured repo+user+keg mapping",
			rawYAML:    []byte("repo: jlr\nuser: jlrickert\nkeg: tapper\n"),
			wantSchema: kegurl.SchemeRegistry,
			wantRepo:   "jlr",
			wantUser:   "jlrickert",
			wantKeg:    "tapper",
		},
		{
			name:       "api: scalar shorthand as yaml string",
			rawYAML:    []byte("jlr:jlrickert/tapper"),
			wantSchema: kegurl.SchemeRegistry,
			wantRepo:   "jlr",
			wantUser:   "jlrickert",
			wantKeg:    "tapper",
		},
		{
			name:       "file: simple path",
			rawYAML:    []byte("/home/testuser/kegs/public"),
			wantSchema: kegurl.SchemeFile,
			wantFile:   "/home/testuser/kegs/public",
		},
		{
			name:       "file: with home expansion",
			rawYAML:    []byte("~/kegs/public"),
			wantSchema: kegurl.SchemeFile,
			wantFile:   "~/kegs/public",
		},
		{
			name:       "file: relative path",
			rawYAML:    []byte("../../kegs/public"),
			wantSchema: kegurl.SchemeFile,
			wantFile:   "../../kegs/public",
		},
		{
			name:       "file: screwy relative path",
			rawYAML:    []byte("..//../kegs/public"),
			wantSchema: kegurl.SchemeFile,
			wantFile:   "../../kegs/public",
		},
		{
			name:       "file: with explicit scheme",
			rawYAML:    []byte("file:///home/testuser/kegs/public"),
			wantSchema: kegurl.SchemeFile,
			wantFile:   "/home/testuser/kegs/public",
		},
		{
			name:       "file: path w/ explicit scheme and home",
			rawYAML:    []byte("file://~/kegs/public"),
			wantSchema: kegurl.SchemeFile,
			wantFile:   "~/kegs/public",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var kt kegurl.Target
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
			if tc.wantRepo != "" {
				require.Equal(t, tc.wantRepo, kt.Repo)
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
			if tc.wantUser != "" {
				require.Equal(t, tc.wantUser, kt.User)
			}
			if tc.wantKeg != "" {
				require.Equal(t, tc.wantKeg, kt.Keg)
			}
			// Ensure the String result is parseable as a URL when non-empty.
			if kt.String() != "" {
				_, err := url.Parse(kt.String())
				require.NoError(t, err, tc.name)
			}
		})
	}
}
