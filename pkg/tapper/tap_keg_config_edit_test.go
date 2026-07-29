package tapper

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

type configWriteCountingRepo struct {
	keg.Repository
	writes int
}

func (r *configWriteCountingRepo) WriteConfig(ctx context.Context, cfg *keg.Config) error {
	r.writes++
	return r.Repository.WriteConfig(ctx, cfg)
}

func TestKegConfigEdit_SeparatesFlightAndIdentityRoles(t *testing.T) {
	t.Parallel()
	identityDenied := errors.New("identity lacks editor access")
	tests := []struct {
		name            string
		flight          *Flight
		identityAllowed bool
		wantErr         string
	}{
		{
			name: "admin cover with editor identity",
			flight: &Flight{Name: "@foldwise/+admin", FlightManifest: FlightManifest{Cover: []FlightCover{{
				Namespace: "foldwise", Keg: "dev", Role: FlightRoleAdmin,
			}}}},
			identityAllowed: true,
		},
		{
			name: "full access with editor identity",
			flight: &Flight{Name: "@foldwise/+full", FlightManifest: FlightManifest{
				Capabilities: []FlightCapability{FlightCapabilityFullAccess},
			}},
			identityAllowed: true,
		},
		{
			name: "editor cover blocks admin operation",
			flight: &Flight{Name: "@foldwise/+editor", FlightManifest: FlightManifest{Cover: []FlightCover{{
				Namespace: "foldwise", Keg: "dev", Role: FlightRoleEditor,
			}}}},
			identityAllowed: true,
			wantErr:         "requires admin flight authority",
		},
		{
			name: "viewer cover blocks admin operation",
			flight: &Flight{Name: "@foldwise/+viewer", FlightManifest: FlightManifest{Cover: []FlightCover{{
				Namespace: "foldwise", Keg: "dev", Role: FlightRoleViewer,
			}}}},
			identityAllowed: true,
			wantErr:         "requires admin flight authority",
		},
		{
			name:            "uncovered keg is blocked",
			flight:          &Flight{Name: "@foldwise/+empty"},
			identityAllowed: true,
			wantErr:         "is not available in flight",
		},
		{
			name: "admin cover cannot overcome viewer identity",
			flight: &Flight{Name: "@foldwise/+admin", FlightManifest: FlightManifest{Cover: []FlightCover{{
				Namespace: "foldwise", Keg: "dev", Role: FlightRoleAdmin,
			}}}},
			wantErr: identityDenied.Error(),
		},
		{
			name: "full access cannot overcome no identity access",
			flight: &Flight{Name: "@foldwise/+full", FlightManifest: FlightManifest{
				Capabilities: []FlightCapability{FlightCapabilityFullAccess},
			}},
			wantErr: identityDenied.Error(),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
			repo := &configWriteCountingRepo{Repository: keg.NewMemoryRepo(sb.Runtime())}
			k := keg.NewLocalKeg(repo, sb.Runtime())
			require.NoError(t, k.Init(t.Context()))
			k.SetTarget(&keg.Target{Namespace: "foldwise", KegName: "dev"})
			repo.writes = 0

			var identityRole FlightRole
			tap, err := NewTap(TapOptions{Root: "/home/testuser", Runtime: sb.Runtime()})
			require.NoError(t, err)
			tap.KegResolver = func(_ context.Context, _ KegTargetOptions, role FlightRole) (keg.Keg, error) {
				identityRole = role
				if !tc.identityAllowed {
					return nil, identityDenied
				}
				return k, nil
			}
			err = tap.KegConfigEdit(t.Context(), KegConfigEditOptions{
				KegTargetOptions: KegTargetOptions{
					Keg:           "@foldwise/dev",
					FlightContext: tc.flight,
				},
				Stream: &toolkit.Stream{
					IsPiped: true,
					In: strings.NewReader(`kegv: 2025-07
title: Edited by agent
`),
				},
			})
			require.Equal(t, FlightRoleEditor, identityRole, "identity authorization must remain editor")
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Zero(t, repo.writes)
				return
			}
			require.NoError(t, err)
			require.Equal(t, 1, repo.writes)
			cfg, readErr := k.Config(t.Context())
			require.NoError(t, readErr)
			require.Equal(t, "Edited by agent", cfg.Title)
		})
	}
}

func TestKegConfigEdit_InvalidAndUnchangedInputDoNotWrite(t *testing.T) {
	t.Parallel()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	repo := &configWriteCountingRepo{Repository: keg.NewMemoryRepo(sb.Runtime())}
	k := keg.NewLocalKeg(repo, sb.Runtime())
	require.NoError(t, k.Init(t.Context()))
	k.SetTarget(&keg.Target{Namespace: "foldwise", KegName: "dev"})
	tap, err := NewTap(TapOptions{Root: "/home/testuser", Runtime: sb.Runtime()})
	require.NoError(t, err)
	tap.KegResolver = func(context.Context, KegTargetOptions, FlightRole) (keg.Keg, error) {
		return k, nil
	}
	flight := &Flight{Name: "@foldwise/+admin", FlightManifest: FlightManifest{Cover: []FlightCover{{
		Namespace: "foldwise", Keg: "dev", Role: FlightRoleAdmin,
	}}}}

	repo.writes = 0
	err = tap.KegConfigEdit(t.Context(), KegConfigEditOptions{
		KegTargetOptions: KegTargetOptions{FlightContext: flight},
		Stream:           &toolkit.Stream{IsPiped: true, In: strings.NewReader("kegv: [\n")},
	})
	require.ErrorContains(t, err, "keg config from stdin is invalid")
	require.Zero(t, repo.writes)

	cfg, err := k.Config(t.Context())
	require.NoError(t, err)
	repo.writes = 0
	err = tap.KegConfigEdit(t.Context(), KegConfigEditOptions{
		KegTargetOptions: KegTargetOptions{FlightContext: flight},
		Stream:           &toolkit.Stream{IsPiped: true, In: strings.NewReader(cfg.String())},
	})
	require.NoError(t, err)
	require.Zero(t, repo.writes, "unchanged config must be a no-op")
}
