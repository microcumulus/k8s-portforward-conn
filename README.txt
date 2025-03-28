This package exists to provide a simple way to forward ports to kubernetes pods
in Go, but as a more fundamental operation that returns a `net.Conn` rather
than forwarding a port on the local operating system as is currently done in
`k8s.io/client-go/tools/portforward`. This is desirable for many reasons:

- These connections may be used internally by Go programs to communicate remotely without
  needing to open a port on the local operating system, improving security (no
  other processes can observe the port forward or connect to it) and reducing
  the likelihood of conflicts or need for local port management.
- If desired, users can still expose the connection locally with net.Listen and 
  io.Copy, or whatever makes the most sense for your use case.

I have had this code running in a production app for a while now, and it works
well. I'm finally putting some effort into releasing it more widely and making
it more robust.

# Usage

```go
package main

import (
  "context"
  "fmt"
  "net"

  k8sport "github.com/microcumulus/k8s-portforward-conn"
  corev1 "k8s.io/api/core/v1"
  metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func main() {
  // Load rest config
  config, err := rest.InClusterConfig()
  if err != nil {
    // handle error
    panic(err)
  }

  // Create a new forwarder
  fwd, err := k8sport.NewForwarder(config)
  if err != nil {
    // handle error
  }

  // Pod spec of the pod you wish to forward to
  pod := corev1.Pod{
    ObjectMeta: metav1.ObjectMeta{
      Namespace: "default",
      Name:      "mypod",
    },
  }

  // Forward to port 8080 on the pod
  conn, err := fwd.Forward(context.Background(), pod, 8080)
  if err != nil {
    // handle error
  }

  cli := conn.HTTPClient()

  res, err := cli.Get("http://doesnnt.matter.com.only.path.gets.used/foo")
  // etc
}

