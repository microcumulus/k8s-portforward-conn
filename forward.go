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
