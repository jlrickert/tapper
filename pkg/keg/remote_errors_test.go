package keg_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
)

func TestRemoteErrorInvalidImageRoundTrip(t *testing.T) {
	code, status := keg.RemoteErrorCode(keg.ErrInvalidImage)
	if code != keg.RemoteCodeInvalidImage {
		t.Fatalf("code = %q, want %q", code, keg.RemoteCodeInvalidImage)
	}
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", status, http.StatusBadRequest)
	}

	err := keg.RemoteErrorFromCode(code, status, "invalid image: could not decode image")
	if !errors.Is(err, keg.ErrInvalidImage) {
		t.Fatalf("round-trip error = %v, want ErrInvalidImage", err)
	}
}

func TestRemoteErrorKegVersionUnsupportedRoundTrip(t *testing.T) {
	code, status := keg.RemoteErrorCode(keg.ErrKegVersionUnsupported)
	if code != keg.RemoteCodeKegVersionUnsupported || status != http.StatusConflict {
		t.Fatalf("got (%q, %d), want (%q, %d)", code, status, keg.RemoteCodeKegVersionUnsupported, http.StatusConflict)
	}
	err := keg.RemoteErrorFromCode(code, status, "node operations are not available for KEGv2 kegs")
	if !errors.Is(err, keg.ErrKegVersionUnsupported) {
		t.Fatalf("round-trip error = %v, want ErrKegVersionUnsupported", err)
	}
	// Callers degrade silently around ErrNotSupported, so a version refusal
	// must never read as an absent optional capability.
	if errors.Is(err, keg.ErrNotSupported) {
		t.Fatalf("version refusal %v must not match ErrNotSupported", err)
	}
}

func TestValidKegVersion(t *testing.T) {
	for v, want := range map[string]bool{
		keg.KegVersionV1: true, keg.KegVersionV2: true,
		"": false, "2027-01": false, " 2025-07": false,
	} {
		if got := keg.ValidKegVersion(v); got != want {
			t.Errorf("ValidKegVersion(%q) = %v, want %v", v, got, want)
		}
	}
}
