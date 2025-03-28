This package exists to provide a simple way to forward ports to kubernetes pods
in Go, but as a more fundamental operation that returns a net.Conn rather than 
forwarding a port on the local operating system. This is desirable for many reasons:

- These connections may be used internally by Go programs to communicate remotely without
  needing to open a port on the local operating system, improving security and
  reducing the likelihood of conflicts or local port management.
- If desired, users can still expose the connection locally with net.Listen and 
  io.Copy, or whatever makes the most sense for your use case.

I have had this code running in a production app for a while now, and it works
well. I'm finally putting some effort into releasing it more widely and making
it more robust.
