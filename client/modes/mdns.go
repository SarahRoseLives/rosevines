package modes

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/grandcat/zeroconf"
)

const (
	DefaultPort   = 9312
	ServiceName   = "_rosevineschat._tcp"
	ServiceDomain = "local."
)

type MDNSMode struct {
	username           string
	port               int
	server             *zeroconf.Server
	resolver           *zeroconf.Resolver
	cancel             context.CancelFunc
	peersLock          sync.Mutex
	peers              map[string]*zeroconf.ServiceEntry
	OnPeerDiscovered   func(name, addr string, count int)
	OnPeerCountChanged func(count int)
	OnMessageReceived  func(user, message string)
}

func NewMDNSMode(username string, port int) *MDNSMode {
	return &MDNSMode{
		username: username,
		port:     port,
		peers:    make(map[string]*zeroconf.ServiceEntry),
	}
}

func (m *MDNSMode) Start() {
	m.Shutdown() // shutdown any previous

	// advertise self
	server, err := zeroconf.Register(m.username, ServiceName, ServiceDomain, m.port, []string{}, nil)
	if err != nil {
		if m.OnMessageReceived != nil {
			m.OnMessageReceived("SYSTEM", fmt.Sprintf("mDNS registration failed: %v", err))
		}
		return
	}
	m.server = server

	// resolve peers
	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		if m.OnMessageReceived != nil {
			m.OnMessageReceived("SYSTEM", fmt.Sprintf("mDNS resolver failed: %v", err))
		}
		return
	}
	m.resolver = resolver

	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	entries := make(chan *zeroconf.ServiceEntry)
	go func() {
		for entry := range entries {
			if entry.Instance != m.username {
				m.peersLock.Lock()
				_, existed := m.peers[entry.Instance]
				m.peers[entry.Instance] = entry
				count := len(m.peers)
				m.peersLock.Unlock()
				if m.OnPeerDiscovered != nil && !existed {
					addr := ""
					if len(entry.AddrIPv4) > 0 {
						addr = entry.AddrIPv4[0].String()
					}
					m.OnPeerDiscovered(entry.Instance, addr, count)
				}
				if m.OnPeerCountChanged != nil {
					m.peersLock.Lock()
					count := len(m.peers)
					m.peersLock.Unlock()
					m.OnPeerCountChanged(count)
				}
			}
		}
	}()
	go func() {
		_ = resolver.Browse(ctx, ServiceName, ServiceDomain, entries)
	}()
	go m.listenBroadcast()
}

func (m *MDNSMode) Broadcast(user, message string) {
	// send message to all discovered peers over UDP
	m.peersLock.Lock()
	defer m.peersLock.Unlock()
	for _, entry := range m.peers {
		if len(entry.AddrIPv4) > 0 {
			addr := entry.AddrIPv4[0]
			go sendUDPMessage(addr.String(), m.port, user, message)
		}
	}
}

func sendUDPMessage(ip string, port int, user, message string) {
	conn, err := net.DialTimeout("udp", fmt.Sprintf("%s:%d", ip, port), 1*time.Second)
	if err == nil {
		// Format: user:message
		msg := fmt.Sprintf("%s:%s", user, message)
		conn.Write([]byte(msg))
		conn.Close()
	}
}

func (m *MDNSMode) listenBroadcast() {
	addr := net.UDPAddr{
		Port: m.port,
		IP:   net.IPv4zero,
	}
	conn, err := net.ListenUDP("udp", &addr)
	if err != nil {
		if m.OnMessageReceived != nil {
			m.OnMessageReceived("SYSTEM", fmt.Sprintf("UDP listener error: %v", err))
		}
		return
	}
	defer conn.Close()
	buf := make([]byte, 2048)
	for {
		n, remote, err := conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		if n > 0 {
			msg := string(buf[:n])
			colon := -1
			for i := range msg {
				if msg[i] == ':' {
					colon = i
					break
				}
			}
			if colon > 0 {
				user := msg[:colon]
				message := msg[colon+1:]
				if user != m.username { // don't echo own messages
					if m.OnMessageReceived != nil {
						m.OnMessageReceived(user, message)
					}
				}
			} else {
				if m.OnMessageReceived != nil {
					m.OnMessageReceived("Peer", fmt.Sprintf("%s (%s)", msg, remote.String()))
				}
			}
		}
	}
}

func (m *MDNSMode) PeerCount() int {
	m.peersLock.Lock()
	defer m.peersLock.Unlock()
	return len(m.peers)
}

func (m *MDNSMode) Shutdown() {
	if m.server != nil {
		m.server.Shutdown()
		m.server = nil
	}
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}