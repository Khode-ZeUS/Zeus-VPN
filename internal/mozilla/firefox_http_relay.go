package mozilla

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"
)

var firefoxHTTPRelay atomic.Pointer[firefoxRelayTransport]

type firefoxRelayTransport struct {
	endpoint  url.URL
	transport *http.Transport
}

func SetFirefoxHTTPRelay(endpoint url.URL, transport *http.Transport) {
	var next *firefoxRelayTransport
	if transport != nil {
		next = &firefoxRelayTransport{endpoint: endpoint, transport: transport}
	}
	if old := firefoxHTTPRelay.Swap(next); old != nil {
		old.transport.CloseIdleConnections()
	}
}

func (r *firefoxRelayTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.User != nil || req.URL.Port() != "" ||
		(req.URL.Hostname() != "accounts.firefox.com" && req.URL.Hostname() != "api.accounts.firefox.com") {
		return nil, errors.New("Firefox sign-in relay refused an unexpected destination")
	}
	out := req.Clone(req.Context())
	endpoint := r.endpoint
	out.URL = &endpoint
	out.Host = ""
	out.Header.Set("X-Zeus-Fxa-Target", req.URL.String())
	if origin := out.Header.Get("Origin"); origin != "" {
		out.Header.Del("Origin")
		out.Header.Set("X-Zeus-Fxa-Origin", origin)
	}
	response, err := r.transport.RoundTrip(out)
	if response != nil {
		if response.Header.Get("X-Zeus-Fxa-Upstream") != "1" {
			response.Body.Close()
			return nil, fmt.Errorf("the Zeus login relay could not reach Firefox Accounts (relay HTTP %d)", response.StatusCode)
		}
		response.Request = req
	}
	return response, err
}
