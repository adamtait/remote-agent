package etconn

import (
	"fmt"
	"net"
	"testing"
)

func TestPickPorts_DistinctAndBindable(t *testing.T) {
	p, err := PickPorts()
	if err != nil {
		t.Fatalf("PickPorts: %v", err)
	}
	if p.Bootstrap == 0 || p.Data == 0 {
		t.Fatalf("PickPorts returned zero port: %+v", p)
	}
	if p.Bootstrap == p.Data {
		t.Fatalf("PickPorts returned identical ports: %+v", p)
	}
	for _, port := range []int{p.Bootstrap, p.Data} {
		l, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
		if err != nil {
			t.Fatalf("port %d not bindable after PickPorts: %v", port, err)
		}
		l.Close()
	}
}
