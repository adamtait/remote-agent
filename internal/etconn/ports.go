package etconn

import "net"

// Ports holds the two local TCP ports the ET connect path forwards to the
// workstation: Bootstrap → ws:22 (ssh handshake) and Data → ws:2022 (etserver).
type Ports struct {
	Bootstrap int
	Data      int
}

// freePort asks the OS for an unused localhost TCP port by binding :0 and
// reading back the assigned port, then releasing it. There is an inherent
// (small) race between releasing the port and the tunnel re-binding it;
// Connect's readiness wait surfaces a clean error if the port is taken in
// between, rather than letting et fail cryptically.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// PickPorts returns two distinct free local ports. Dynamic selection (rather
// than fixed 22/2022) avoids collisions with other local services and lets a
// second `ra connect --et` to a different workstation run concurrently.
func PickPorts() (Ports, error) {
	bootstrap, err := freePort()
	if err != nil {
		return Ports{}, err
	}
	data, err := freePort()
	if err != nil {
		return Ports{}, err
	}
	// The OS can hand back the same port twice across the two binds; retry
	// until they differ so the two tunnels don't collide.
	for data == bootstrap {
		if data, err = freePort(); err != nil {
			return Ports{}, err
		}
	}
	return Ports{Bootstrap: bootstrap, Data: data}, nil
}
