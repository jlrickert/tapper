// Package tapper — hub keg-catalog client calls.
//
// These talk to a remote hub's namespace/keg-catalog endpoints (as opposed to
// the per-keg operation API in pkg/keg/keg_remote.go): creating a keg and listing the
// kegs a user can reach. They mirror the slim, dependency-free shape of
// ValidateToken (auth_whoami.go) — a stand-alone function over
// http.DefaultClient so tests can point hubURL at an httptest.Server.
package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jlrickert/tapper/pkg/apicontract"
	"github.com/jlrickert/tapper/pkg/keg"
)

// hubKegsPath is the user-scoped keg listing: every keg the bearer token can
// reach across all namespaces on the hub (membership + grants). Mirrors the
// hub's GET /api/v1/kegs handler (handler.ListUserKegs).
const hubKegsPath = "/api/v1/kegs"

// HubKeg is one keg the hub reports the authenticated user can reach. It
// mirrors the hub's handler.UserKegItem JSON body — keep the two in sync.
type HubKeg struct {
	Namespace   string `json:"namespace"`
	Alias       string `json:"alias"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Visibility  string `json:"visibility"`
	Role        string `json:"role"`
	// KegVersion is the KEG's immutable storage version (keg.KegVersionV1 or
	// keg.KegVersionV2). Hubs that predate versioned KEGs omit it; an empty
	// value reads as KEGv1.
	KegVersion string `json:"kegv,omitempty"`
}

// HubKegCreate is the body of the Hub's keg-create request. An empty
// KegVersion is omitted so the Hub applies its default (KEGv1), which keeps
// the request acceptable to Hubs that predate versioned KEGs.
type HubKegCreate struct {
	Alias      string `json:"alias"`
	Title      string `json:"title"`
	Visibility string `json:"visibility"`
	KegVersion string `json:"kegv,omitempty"`
}

// CreateKeg asks the hub to create @namespace/alias with the Hub's default
// KEG version. See CreateHubKeg.
func CreateKeg(ctx context.Context, hubURL, token, namespace, alias, title, visibility string) error {
	return CreateHubKeg(ctx, hubURL, token, namespace, HubKegCreate{Alias: alias, Title: title, Visibility: visibility})
}

// CreateHubKeg asks the hub to create a KEG in namespace via
// POST /api/v1/@{namespace}/kegs. A 409 maps by the error envelope's code:
// KEG_VERSION_UNSUPPORTED (the Hub does not offer the requested KEG version,
// such as KEGv2 while its preview is off) wraps keg.ErrKegVersionUnsupported,
// and EXIST, CONFLICT, or a 409 without a code wraps keg.ErrExist so callers
// can detect "already exists" with errors.Is. 400 wraps keg.ErrInvalid (an unknown KEG
// version, for example); 401 wraps ErrTokenRejected; 403 wraps
// keg.ErrForbidden; other non-2xx statuses surface the hub's status line.
func CreateHubKeg(ctx context.Context, hubURL, token, namespace string, in HubKegCreate) error {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("hub: encode create-keg request: %w", err)
	}
	alias := in.Alias
	endpoint := fmt.Sprintf("%s/api/v1/@%s/kegs", base, namespace)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("hub: build create-keg request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := apicontract.Do(hubHTTPClient(), req)
	if err != nil {
		return fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusCreated, http.StatusOK:
		return checkCreatedKegVersion(resp, namespace, alias, in.KegVersion)
	case http.StatusConflict:
		code, msg := readHubErrorEnvelope(resp)
		// Hubs that predate the EXIST code report a duplicate KEG as a
		// generic CONFLICT, so both still mean "already exists".
		if code == "" || code == keg.RemoteCodeExist || code == keg.RemoteCodeConflict {
			return fmt.Errorf("hub: keg @%s/%s already exists: %w", namespace, alias, keg.ErrExist)
		}
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("hub: create keg @%s/%s: %w", namespace, alias, keg.RemoteErrorFromCode(code, resp.StatusCode, msg))
	case http.StatusBadRequest:
		return fmt.Errorf("hub: create keg @%s/%s: %w%s", namespace, alias, keg.ErrInvalid, readHubError(resp))
	case http.StatusForbidden:
		return fmt.Errorf("hub: %w (%s)%s", keg.ErrForbidden, resp.Status, readHubError(resp))
	case http.StatusUnauthorized:
		return fmt.Errorf("hub: %w (%s)", ErrTokenRejected, resp.Status)
	default:
		return fmt.Errorf("hub: create keg @%s/%s failed: %s%s", namespace, alias, resp.Status, readHubError(resp))
	}
}

// KegVersionNotAppliedError reports a KEG the Hub created at a version other
// than the one requested. A Hub that predates versioned KEGs ignores the
// requested version and creates a KEGv1 KEG. The KEG exists: the error
// reports the outcome and never deletes it. It unwraps to
// keg.ErrKegVersionUnsupported.
type KegVersionNotAppliedError struct {
	Namespace string
	Alias     string
	Requested string
	Created   string
}

func (e *KegVersionNotAppliedError) Error() string {
	return fmt.Sprintf("hub: created @%s/%s as %s (%s), not the requested %s (%s), because the Hub does not support KEG version %s; "+
		"the KEG exists: use it as %s or delete it",
		e.Namespace, e.Alias, kegVersionName(e.Created), e.Created, kegVersionName(e.Requested), e.Requested, e.Requested, kegVersionName(e.Created))
}

func (e *KegVersionNotAppliedError) Unwrap() error { return keg.ErrKegVersionUnsupported }

// kegVersionName names a KEG version for messages.
func kegVersionName(v string) string {
	if label := KegVersionLabel(v); label != "" {
		return label
	}
	if v == keg.KegVersionV1 {
		return "KEGv1"
	}
	return "KEG version " + v
}

// checkCreatedKegVersion compares the version the Hub reports in its create
// response with the requested one. A Hub that predates versioned KEGs reports
// no version and created KEGv1, which satisfies only a KEGv1 request.
func checkCreatedKegVersion(resp *http.Response, namespace, alias, requested string) error {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return nil
	}
	var created struct {
		KegVersion string `json:"kegv"`
	}
	if b, err := io.ReadAll(io.LimitReader(resp.Body, 4096)); err == nil {
		_ = json.Unmarshal(b, &created)
	}
	got := strings.TrimSpace(created.KegVersion)
	if got == "" {
		got = keg.KegVersionV1
	}
	if got == requested {
		return nil
	}
	return &KegVersionNotAppliedError{Namespace: namespace, Alias: alias, Requested: requested, Created: got}
}

// ListUserKegs returns every keg the hub reports the bearer token can reach
// (namespace membership + grants) via GET /api/v1/kegs.
func ListUserKegs(ctx context.Context, hubURL, token string) ([]HubKeg, error) {
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+hubKegsPath, nil)
	if err != nil {
		return nil, fmt.Errorf("hub: build list-kegs request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")

	resp, err := apicontract.Do(hubHTTPClient(), req)
	if err != nil {
		return nil, fmt.Errorf("hub: contact hub: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusOK:
		// fall through to decode
	case http.StatusForbidden:
		return nil, fmt.Errorf("hub: %w (%s)%s", keg.ErrForbidden, resp.Status, readHubError(resp))
	case http.StatusUnauthorized:
		return nil, fmt.Errorf("hub: %w (%s)", ErrTokenRejected, resp.Status)
	default:
		return nil, fmt.Errorf("hub: list kegs returned %s for %s", resp.Status, hubKegsPath)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("hub: read list-kegs response: %w", err)
	}
	var kegs []HubKeg
	if err := json.Unmarshal(body, &kegs); err != nil {
		return nil, fmt.Errorf("hub: parse list-kegs response: %w", err)
	}
	return kegs, nil
}

// readHubError best-effort extracts the hub's {"error": ...} message from a
// failed response so the surfaced error carries the hub's own explanation.
func readHubError(resp *http.Response) string {
	if _, msg := readHubErrorEnvelope(resp); msg != "" {
		return ": " + msg
	}
	return ""
}

// readHubErrorEnvelope best-effort decodes the hub's {"error", "code"}
// envelope from a failed response. Either value is empty when absent.
func readHubErrorEnvelope(resp *http.Response) (code, msg string) {
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil || len(b) == 0 {
		return "", ""
	}
	var e struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}
	if json.Unmarshal(b, &e) != nil {
		return "", ""
	}
	return strings.TrimSpace(e.Code), strings.TrimSpace(e.Error)
}

// hubHTTPClient refuses redirects so credentials and mutations stay on the selected Hub.
func hubHTTPClient() *http.Client {
	client := *http.DefaultClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

// UnmarshalJSON accepts summary from older Hubs without overriding an explicit description.
func (k *HubKeg) UnmarshalJSON(data []byte) error {
	type plain HubKeg
	var v plain
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	if _, ok := fields["description"]; !ok {
		if raw, exists := fields["summary"]; exists {
			if err := json.Unmarshal(raw, &v.Description); err != nil {
				return err
			}
		}
	}
	*k = HubKeg(v)
	return nil
}

// DeleteKeg permanently deletes a KEG and all dependent data, including snapshots.
// There is no expected hash: the settings hash does not cover a whole KEG.
func DeleteKeg(ctx context.Context, hubURL, token, namespace, alias string) error {
	if !hubKegAliasPattern.MatchString(namespace) || !hubKegAliasPattern.MatchString(alias) {
		return fmt.Errorf("%w: invalid canonical KEG reference", keg.ErrInvalid)
	}
	base, err := normalizeHubURL(hubURL)
	if err != nil {
		return err
	}
	endpoint := fmt.Sprintf("%s/api/v1/@%s/kegs/%s", base, namespace, alias)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if value, ok := keg.OrientationHeaderForURL(ctx, endpoint); ok {
		req.Header.Set(keg.OrientationHeaderName, value)
	}
	resp, err := apicontract.Do(hubHTTPClient(), req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return nil
	case http.StatusNotFound:
		return fmt.Errorf("hub: %w", keg.ErrNotExist)
	case http.StatusUnauthorized:
		return fmt.Errorf("hub: %w", ErrTokenRejected)
	case http.StatusForbidden:
		return fmt.Errorf("hub: %w%s", keg.ErrForbidden, readHubError(resp))
	default:
		return fmt.Errorf("hub: delete keg failed: %s%s", resp.Status, readHubError(resp))
	}
}
