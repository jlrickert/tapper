package keg

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
)

type orientationRoundTripFunc func(*http.Request) (*http.Response, error)

func (f orientationRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestRemoteKegCarriesTrustedOrientationHeader(t *testing.T) {
	state := OrientationState{Root: "@team/+root", Active: "@team/+child", Revision: "revision"}
	ctx := WithOrientationState(context.Background(), state)
	remote := NewRemoteKeg("https://hub.test/api/v1/@team/kegs/dev", "token", nil)
	remote.client = &http.Client{Transport: orientationRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		decoded, err := DecodeOrientationState(req.Header.Get(OrientationHeaderName))
		if err != nil {
			t.Fatalf("DecodeOrientationState: %v", err)
		}
		if decoded != state {
			t.Fatalf("orientation header = %+v, want %+v", decoded, state)
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header)}, nil
	})}
	resp, err := remote.do(ctx, http.MethodGet, "/nodes", nil, "", http.Header{OrientationHeaderName: []string{"model-supplied"}})
	if err != nil {
		t.Fatalf("remote do: %v", err)
	}
	_ = resp.Body.Close()
}

func TestDecodeOrientationStateRejectsIncompleteAndUnknownVersions(t *testing.T) {
	for _, raw := range []string{"", "v2.e30", "v1.e30", "v1.not-base64"} {
		if _, err := DecodeOrientationState(raw); err == nil {
			t.Fatalf("DecodeOrientationState(%q) succeeded", raw)
		}
	}
}
