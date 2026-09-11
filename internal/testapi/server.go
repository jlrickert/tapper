// Package testapi supplies the mandatory contract gate and discovery for mock Hub fixtures.
package testapi

import (
	"github.com/jlrickert/tapper/pkg/apicontract"
	"net/http"
	"net/http/httptest"
)

func NewServer(h http.Handler) *httptest.Server {
	return httptest.NewServer(apicontract.Middleware("test", nil)(h))
}
func NewTLSServer(h http.Handler) *httptest.Server {
	return httptest.NewTLSServer(apicontract.Middleware("test", nil)(h))
}
func NewUnstartedServer(h http.Handler) *httptest.Server {
	return httptest.NewUnstartedServer(apicontract.Middleware("test", nil)(h))
}
