package cli_test

import (
	"encoding/json"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

type indexTestCase struct {
	name             string
	args             []string
	setupFixture     *string
	cwd              *string
	expectedInStdout []string
	expectedErr      string
	description      string
}

func TestIndexCommand_TableDrivenErrorHandling(t *testing.T) {
	tests := []indexTestCase{
		{
			name:         "index_nonexistent_alias",
			args:         []string{"index", "--alias", "nonexistent"},
			setupFixture: strPtr("joe"),
			expectedErr:  "keg alias not found",
			description:  "Error when keg alias does not exist",
		},
		{
			name:        "index_no_keg_configured",
			args:        []string{"index"},
			expectedErr: "no keg configured",
			description: "Error when no keg is configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerT *testing.T) {
			innerT.Parallel()
			var opts []testutils.Option
			if tt.setupFixture != nil {
				opts = append(opts, testutils.WithFixture(*tt.setupFixture, "~"))
			}
			sb := NewSandbox(innerT, opts...)

			if tt.cwd != nil {
				sb.Setwd(*tt.cwd)
			}

			h := NewProcess(innerT, false, tt.args...)
			res := h.Run(sb.Context(), sb.Runtime())

			require.Error(innerT, res.Err, "expected error - %s", tt.description)
			stderr := string(res.Stderr)
			require.Contains(innerT, stderr, tt.expectedErr,
				"error message should contain %q, got stderr: %s and err: %v", tt.expectedErr, stderr, res.Err)
		})
	}
}

func TestIndexCommand_WithJoeFixture(t *testing.T) {
	tests := []indexTestCase{
		{
			name:             "index_personal_keg_from_default_location",
			args:             []string{"index"},
			setupFixture:     strPtr("joe"),
			expectedInStdout: []string{"Indices rebuilt"},
			description:      "Rebuild indices for default personal keg",
		},
		{
			name:             "index_work_keg_from_work_directory",
			args:             []string{"index"},
			setupFixture:     strPtr("joe"),
			cwd:              strPtr("~/repos/work/spy-things"),
			expectedInStdout: []string{"Indices rebuilt"},
			description:      "Rebuild indices for work keg when in work directory",
		},
		{
			name:             "index_explicit_alias_overrides_path_resolution",
			args:             []string{"index", "--alias", "example"},
			setupFixture:     strPtr("joe"),
			cwd:              strPtr("~/repos/work/spy-things"),
			expectedInStdout: []string{"Indices rebuilt"},
			description:      "Explicit alias overrides path-based keg resolution",
		},
		{
			name:             "index_personal_keg_explicit_alias",
			args:             []string{"index", "--alias", "personal"},
			setupFixture:     strPtr("joe"),
			expectedInStdout: []string{"Indices rebuilt"},
			description:      "Rebuild indices for personal keg with explicit alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerT *testing.T) {
			innerT.Parallel()
			var opts []testutils.Option
			if tt.setupFixture != nil {
				opts = append(opts, testutils.WithFixture(*tt.setupFixture, "~"))
			}
			sb := NewSandbox(innerT, opts...)

			if tt.cwd != nil {
				sb.Setwd(*tt.cwd)
			}

			h := NewProcess(innerT, false, tt.args...)
			res := h.Run(sb.Context(), sb.Runtime())

			require.NoError(innerT, res.Err, "index command should succeed - %s", tt.description)
			stdout := string(res.Stdout)

			for _, expected := range tt.expectedInStdout {
				require.Contains(innerT, stdout, expected,
					"expected output to contain %q, got:\n%s", expected, stdout)
			}
		})
	}
}

func TestIndexCommand_IntegrationWithInit(t *testing.T) {
	t.Run("index_after_init", func(innerT *testing.T) {
		innerT.Parallel()
		opts := []testutils.Option{
			testutils.WithFixture("testuser", "~"),
		}
		sb := NewSandbox(innerT, opts...)

		// First, initialize a user keg
		initCmd := NewProcess(innerT, false,
			"repo", "init",
			"newstudy",
			"--user",
			"--keg", "newstudy",
			"--creator", "test-user",
		)
		initRes := initCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, initRes.Err, "init should succeed")
		require.Contains(innerT, string(initRes.Stdout), "keg newstudy created")

		// Now rebuild indices
		indexCmd := NewProcess(innerT, false, "index", "--alias", "newstudy")
		indexRes := indexCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, indexRes.Err, "index should succeed")

		stdout := string(indexRes.Stdout)
		require.Contains(innerT, stdout, "Indices rebuilt", "output should indicate successful rebuild")
	})
}

type statsJSON struct {
	Title   string   `json:"title"`
	Hash    string   `json:"hash"`
	Updated string   `json:"updated"`
	Created string   `json:"created"`
	Lead    string   `json:"lead"`
	Links   []string `json:"links"`
}

func TestIndexCommand_CreatesMissingMetaAndStatsFiles(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	metaPath := "~/kegs/example/0/meta.yaml"
	statsPath := "~/kegs/example/0/stats.json"

	require.NoError(t, sb.Runtime().Remove(metaPath, false))
	_ = sb.Runtime().Remove(statsPath, false)

	h := NewProcess(t, false, "index", "--alias", "example")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "index should repair missing node files")

	_, err := sb.Runtime().Stat(metaPath, false)
	require.NoError(t, err, "meta.yaml should be recreated")
	_, err = sb.Runtime().Stat(statsPath, false)
	require.NoError(t, err, "stats.json should be recreated")

	statsRaw := sb.MustReadFile(statsPath)
	var got statsJSON
	require.NoError(t, json.Unmarshal(statsRaw, &got))
	require.NotEmpty(t, got.Title)
	require.NotEmpty(t, got.Hash)
	require.NotEmpty(t, got.Updated)
	require.NotEmpty(t, got.Created)
	require.NotEmpty(t, got.Lead)
}

func TestIndexCommand_UpdatesStatsFromNodeContent(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	contentPath := "~/kegs/example/0/README.md"
	statsPath := "~/kegs/example/0/stats.json"
	oldUpdated := "2001-01-01T00:00:00Z"
	oldCreated := "2001-01-01T00:00:00Z"

	bogus := []byte(`{"title":"WRONG","hash":"bad-hash","updated":"` + oldUpdated + `","created":"` + oldCreated + `","lead":"wrong lead","links":["9999"]}`)
	sb.MustWriteFile(statsPath, bogus, 0o644)

	h := NewProcess(t, false, "index", "--alias", "example")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "index should refresh stale stats")

	contentRaw := sb.MustReadFile(contentPath)
	parsed, err := keg.ParseContent(sb.Runtime(), contentRaw, keg.FormatMarkdown)
	require.NoError(t, err)

	statsRaw := sb.MustReadFile(statsPath)
	var got statsJSON
	require.NoError(t, json.Unmarshal(statsRaw, &got))

	require.Equal(t, parsed.Title, got.Title, "title should be derived from content")
	require.Equal(t, parsed.Hash, got.Hash, "hash should match content hash")
	require.Equal(t, parsed.Lead, got.Lead, "lead should be derived from content")
	require.NotEqual(t, oldUpdated, got.Updated, "updated timestamp should move forward")
	require.Equal(t, oldCreated, got.Created, "created timestamp should be preserved")
	require.Empty(t, got.Links, "links should reflect parsed content")
}

func TestIndexCommand_CreatesDexArtifactsWhenMissing(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	dexDir := "~/kegs/example/dex"
	require.NoError(t, sb.Runtime().Remove(dexDir, true))

	h := NewProcess(t, false, "index", "--alias", "example")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "index should recreate dex artifacts")

	for _, path := range []string{
		"~/kegs/example/dex/nodes.tsv",
		"~/kegs/example/dex/tags",
		"~/kegs/example/dex/links",
		"~/kegs/example/dex/backlinks",
	} {
		_, err := sb.Runtime().Stat(path, false)
		require.NoError(t, err, "expected dex artifact to exist: %s", path)
	}
}

func TestIndexCommand_FailsOnMalformedMeta(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	metaPath := "~/kegs/example/0/meta.yaml"
	sb.MustWriteFile(metaPath, []byte("title: [\n"), 0o644)

	h := NewProcess(t, false, "index", "--alias", "example")
	res := h.Run(sb.Context(), sb.Runtime())

	require.Error(t, res.Err, "index should fail for malformed meta")
	stderr := string(res.Stderr)
	require.Contains(t, stderr, "unable to rebuild indices")
	require.Contains(t, stderr, "failed to parse meta yaml")
}
