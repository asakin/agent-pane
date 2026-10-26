package cmd

import (
	"net"
	"testing"
)

func TestListenLoopback(t *testing.T) {
	ln, err := listenLoopback()
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("addr type %T", ln.Addr())
	}
	if !tcp.IP.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("bound %s", tcp.IP)
	}
	if tcp.Port == 0 {
		t.Fatal("port is 0")
	}
}
