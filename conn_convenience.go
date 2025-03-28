package k8sport

import (
	"context"
	"net"
	"net/http"
)

// HTTPTransport returns an http.Transport that uses the FwdConn as the
// underlying connection. Note: it will always reuse the same conn
func (f *FwdConn) HTTPTransport() *http.Transport {
	return &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return f, nil
		},
	}
}

// HTTPClient returns an http.Client that uses the FwdConn as its transport.
func (f *FwdConn) HTTPClient() *http.Client {
	return &http.Client{Transport: f.HTTPTransport()}
}
