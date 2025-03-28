package k8sport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
)

const networkName = "port-forward"

type fwdAddr string

func (f fwdAddr) Network() string {
	return networkName
}

func (f fwdAddr) String() string {
	return string(f)
}

type fwdConn struct {
	fwd       httpstream.Connection
	data, err httpstream.Stream
	errch     chan error
	port      string
	pod       v1.Pod
}

func (f *fwdConn) watchErr(ctx context.Context) {
	// This should only return if an err comes back
	bs, err := io.ReadAll(f.err)
	if err != nil {
		select {
		case <-ctx.Done():
		case f.errch <- fmt.Errorf("error while reading error stream: %w", err):
		}
	}
	if len(bs) > 0 {
		select {
		case <-ctx.Done():
		case f.errch <- fmt.Errorf("error returned from apiserver: %s", string(bs)):
		}
	}
}

// Read first checks if there is an error on the error stream. If there is, it
// returns it. Otherwise, it reads from the data stream.
func (f *fwdConn) Read(b []byte) (n int, err error) {
	select {
	case err := <-f.errch:
		return 0, err
	default:
	}
	return f.data.Read(b)
}

// Write first checks if there is an error on the error stream. If there is, it
// returns it. Otherwise, it writes to the data stream.
func (f *fwdConn) Write(b []byte) (n int, err error) {
	select {
	case err := <-f.errch:
		return 0, err
	default:
	}
	return f.data.Write(b)
}

// Close closes the connection, removing the streams and closing the forwarder.
// It returns an error if any of the operations fail.
func (f *fwdConn) Close() error {
	var errs []error
	select {
	case err := <-f.errch:
		if err != nil {
			errs = append(errs, err)
		}
	default:
	}
	err := f.data.Close()
	if err != nil {
		errs = append(errs, err)
	}
	f.fwd.RemoveStreams(f.data, f.err)
	err = f.fwd.Close()
	if err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// LocalAddr returns the local network address, if known.
func (f *fwdConn) LocalAddr() net.Addr {
	return fwdAddr(networkName + ":" + f.port)
}

func (f *fwdConn) RemoteAddr() net.Addr {
	return fwdAddr(fmt.Sprintf("k8s/%s/%s:%s", f.pod.Namespace, f.pod.Name, f.port))
}

func (f *fwdConn) SetDeadline(t time.Time) error {
	f.fwd.SetIdleTimeout(time.Until(t))
	return nil
}

func (f *fwdConn) SetReadDeadline(t time.Time) error {
	f.fwd.SetIdleTimeout(time.Until(t))
	return nil
}

func (f *fwdConn) SetWriteDeadline(t time.Time) error {
	f.fwd.SetIdleTimeout(time.Until(t))
	return nil
}
