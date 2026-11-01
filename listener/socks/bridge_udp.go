package socks

import (
	"errors"
	"net"
	"sync"

	"github.com/metacubex/mihomo/adapter/inbound"
	C "github.com/metacubex/mihomo/constant"
	"github.com/metacubex/mihomo/transport/socks5"
)

// bridgeUDPAssociation makes SOCKS5 UDP relay identity follow the authenticated
// TCP control session. The server replies with a per-session ephemeral UDP port.
// The socket is closed when the TCP control connection ends.
//
// This is opt-in, not a change to the upstream default UDP SOCKS listener.
type bridgeUDPAssociation struct {
	conn    *net.UDPConn
	once    sync.Once
}

func (a *bridgeUDPAssociation) LocalAddr() net.Addr { return a.conn.LocalAddr() }
func (a *bridgeUDPAssociation) Close() { a.once.Do(func() { _ = a.conn.Close() }) }

func newBridgeUDPAssociation(control net.Conn, request socks5.Addr, tunnel C.Tunnel, user string, additions []inbound.Addition) (*bridgeUDPAssociation, error) {
	peer, ok := control.RemoteAddr().(*net.TCPAddr)
	if !ok || peer.IP == nil {
		return nil, errors.New("UDP association needs a TCP peer IP")
	}
	local, ok := control.LocalAddr().(*net.TCPAddr)
	if !ok || local.IP == nil {
		return nil, errors.New("UDP association needs a TCP local IP")
	}
	// SOCKS5 clients may request an explicit UDP source port or 0 (unknown).
	requestPort := 0
	if requested := request.UDPAddr(); requested != nil {
		requestPort = requested.Port
		if !requested.IP.IsUnspecified() && !requested.IP.Equal(peer.IP) {
			return nil, errors.New("UDP association source IP disagrees with TCP peer")
		}
	}
	udpConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: local.IP, Port: 0})
	if err != nil { return nil, err }
	a := &bridgeUDPAssociation{conn: udpConn}
	additions = append(append([]inbound.Addition(nil), additions...), inbound.WithInUser(user))
	go func() {
		// Port 0 is legal in UDP ASSOCIATE: pin the source port on first valid
		// datagram. A unique server port prevents identity mixing between
		// simultaneous associations on the same TCP listener.
		pinnedPort := requestPort
		for {
			buf := make([]byte, 65535)
			n, src, err := udpConn.ReadFromUDP(buf)
			if err != nil { return }
			if !src.IP.Equal(peer.IP) { continue }
			if pinnedPort != 0 && src.Port != pinnedPort { continue }
			if _, _, err = socks5.DecodeUDPPacket(buf[:n]); err != nil { continue }
			if pinnedPort == 0 { pinnedPort = src.Port }
			handleSocksUDP(udpConn, tunnel, buf[:n], nil, src, additions...)
		}
	}()
	return a, nil
}
