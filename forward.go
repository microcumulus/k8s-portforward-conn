package k8sport

import (
	"context"
	"fmt"
	"net"
	"net/http"

	"go.opentelemetry.io/otel"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// Forward establishes a port forwarding connection to a specified pod in a Kubernetes cluster.
// It takes a Kubernetes REST configuration, a pod object, and a port number as input.
// The function returns a net.Conn representing the established connection, or an error if the connection fails to be established.
//
// Parameters:
//   - ctx: A context.Context for managing the lifecycle of the port forwarding operation.
//   - rc: A *rest.Config object containing the Kubernetes cluster configuration.
//   - pod: A corev1.Pod object representing the pod to forward ports to.
//   - port: A string representing the port number to forward (e.g., "8080").
//
// Usage:
//
//	conn, err := Forward(ctx, restConfig, myPod, "8080")
//	if err != nil {
//		log.Fatalf("Error forwarding port: %v", err)
//		return
//	}
//	defer conn.Close()
//
// The returned net.Conn can then be used to send and receive data to the specified port on the pod.
func Forward(ctx context.Context, rc *rest.Config, pod corev1.Pod, port string) (net.Conn, error) {
	ctx, sp := otel.Tracer("vault.go").Start(ctx, "portForward")
	defer sp.End()

	cs, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("error creating http client: %w", err)
	}

	req := cs.RESTClient().
		Post().
		Prefix("api/v1").
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(rc)
	if err != nil {
		return nil, fmt.Errorf("error creating spdy roundtripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())
	conn, _, err := dialer.Dial(portforward.PortForwardProtocolV1Name)
	if err != nil {
		return nil, fmt.Errorf("error dialing for stream: %w", err)
	}

	headers := http.Header{}
	headers.Set(v1.StreamType, v1.StreamTypeError)
	headers.Set(v1.PortHeader, port)
	headers.Set(v1.PortForwardRequestIDHeader, "1")

	errorStream, err := conn.CreateStream(headers)
	if err != nil {
		return nil, fmt.Errorf("error creating err stream: %w", err)
	}
	// we're not writing to this stream
	errorStream.Close()

	headers.Set(v1.StreamType, v1.StreamTypeData)
	dataStream, err := conn.CreateStream(headers)
	if err != nil {
		return nil, fmt.Errorf("error creating data stream: %w", err)
	}

	fc := &fwdConn{
		fwd:   conn,
		port:  port,
		err:   errorStream,
		errch: make(chan error),
		data:  dataStream,
		pod:   pod,
	}
	go fc.watchErr(ctx)

	return fc, nil
}
