package socks5

import (
	"net"
	"testing"

	"github.com/metacubex/mihomo/component/auth"
)

func TestUDPAssociateCustomReply(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil { t.Fatal(err) }
	defer listener.Close()
	result := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil { result <- err; return }
		defer conn.Close()
		_, command, username, err := ServerHandshakeWithReply(conn, auth.AlwaysValid,
			func(command Command, user string, request Addr) (net.Addr, error) {
				if command != CmdUDPAssociate || user != "alice" {
					t.Errorf("unexpected association: command=%d, user=%q", command, user)
				}
				return &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 30123}, nil
			})
		if command != CmdUDPAssociate || username != "alice" {
			t.Errorf("unexpected authenticated association: %d %s", command, username)
		}
		result <- err
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil { t.Fatal(err) }
	defer client.Close()
	bound, err := ClientHandshake(client, ParseAddr("0.0.0.0:0"), CmdUDPAssociate,
		&User{Username: "alice", Password: "test"})
	if err != nil { t.Fatal(err) }
	address := bound.UDPAddr()
	if address == nil || address.Port != 30123 {
		t.Fatalf("expected isolated UDP port 30123, got %s", bound.String())
	}
	if err := <-result; err != nil { t.Fatal(err) }
}
