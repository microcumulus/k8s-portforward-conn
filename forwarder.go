package k8sport

import (
	"fmt"
	"net/http"
	"sync/atomic"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/transport/spdy"
)

var (
	ErrRestConfigInvalid = fmt.Errorf("rest config is invalid")
)

type Forwarder struct {
	kc        rest.Interface
	transport http.RoundTripper
	upgrader  spdy.Upgrader

	reqID atomic.Int32
}

// NewForwarder takes a Kubernetes REST configuration and returns a new
// Forwarder instance. This instance can be used to establish port forwarding
// connections to pods in the Kubernetes cluster reusing an underlying SPDY dialer.
func NewForwarder(rc *rest.Config) (*Forwarder, error) {
	cs, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrRestConfigInvalid, err)
	}

	transport, upgrader, err := spdy.RoundTripperFor(rc)
	if err != nil {
		return nil, fmt.Errorf("error creating spdy roundtripper: %w", err)
	}

	return &Forwarder{
		kc:        cs.RESTClient(),
		transport: transport,
		upgrader:  upgrader,
	}, nil
}
