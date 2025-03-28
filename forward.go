package k8sport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// Forward establishes a port forwarding connection to the specified pod on the given port.
// It returns a net.Conn representing the connection to the pod, or an error if the connection could not be established.
func (fw *Forwarder) Forward(ctx context.Context, pod corev1.Pod, port string) (net.Conn, error) {
	req := fw.kc.Post().
		Prefix("api/v1").
		Resource("pods").
		Name(pod.Name).
		Namespace(pod.Namespace).
		SubResource("portforward")

	dialer := spdy.NewDialer(fw.upgrader, &http.Client{Transport: fw.transport}, "POST", req.URL())
	conn, _, err := dialer.Dial(portforward.PortForwardProtocolV1Name)
	if err != nil {
		return nil, fmt.Errorf("error dialing for stream: %w", err)
	}

	headers := http.Header{}
	headers.Set(v1.StreamType, v1.StreamTypeError)
	headers.Set(v1.PortHeader, port)

	next := fw.reqID.Add(1)
	headers.Set(v1.PortForwardRequestIDHeader, strconv.Itoa(int(next)))

	errorStream, err := conn.CreateStream(headers)
	if err != nil {
		return nil, fmt.Errorf("error creating err stream: %w", err)
	}
	// We won't need to write to this.
	errorStream.Close()

	headers.Set(corev1.StreamType, corev1.StreamTypeData)
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
