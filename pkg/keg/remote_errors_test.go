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
