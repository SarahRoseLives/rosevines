package modes

import (
	"context"
	"fmt"
	"net"
	"sync"
	"time"
)

type DirectMode struct {
	username           string
	addr               string
	port               int
	mode               string // "server" or "client"
	listener           net.Listener
	conn               net.Conn
	connected          bool
	cancel             context.CancelFunc
	OnMessageReceived  func(user, message string)
	OnStatus           func(msg string)
	OnConnected        func()
	sendQueue          chan string
	wg                 sync.WaitGroup
}

func NewDirectMode(username, addr string, port int, mode string) *DirectMode {
	return &DirectMode{
		username:  username,
		addr:      addr,
		port:      port,
		mode:      mode,
		sendQueue: make(chan string, 10),
	}
}

func (d *DirectMode) Start() {
	d.Shutdown()
	switch d.mode {
	case "client":
		go d.runClient()
	case "server":
		go d.runServer()
	default:
		if d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("Invalid direct mode: %s. Use 'client' or 'server'.", d.mode))
		}
	}
}

// Client mode: connect to a peer
func (d *DirectMode) runClient() {
	target := fmt.Sprintf("%s:%d", d.addr, d.port)
	if d.OnStatus != nil {
		d.OnStatus(fmt.Sprintf("Trying to connect to peer at %s...", target))
	}
	conn, err := net.Dial("tcp", target)
	if err != nil {
		if d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("Failed to connect to peer at %s: %v", target, err))
		}
		return
	}
	d.conn = conn
	d.connected = true
	if d.OnConnected != nil {
		d.OnConnected()
	}
	if d.OnStatus != nil {
		d.OnStatus(fmt.Sprintf("Connected to peer at %s", target))
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.wg.Add(2)
	go d.directReadLoop(ctx)
	go d.directWriteLoop(ctx)
}

// Server mode: listen for a connection
func (d *DirectMode) runServer() {
	listenAddr := fmt.Sprintf(":%d", d.port)
	if d.OnStatus != nil {
		d.OnStatus(fmt.Sprintf("Listening for a direct peer on port %d...", d.port))
	}
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		if d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("Failed to listen on port %d: %v", d.port, err))
		}
		return
	}
	d.listener = ln
	conn, err := ln.Accept()
	if err != nil {
		if d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("Accept error: %v", err))
		}
		return
	}
	d.conn = conn
	d.connected = true
	if d.OnConnected != nil {
		d.OnConnected()
	}
	if d.OnStatus != nil {
		d.OnStatus(fmt.Sprintf("Peer connected from %s", conn.RemoteAddr().String()))
	}
	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.wg.Add(2)
	go d.directReadLoop(ctx)
	go d.directWriteLoop(ctx)
}

func (d *DirectMode) SendMessage(user, message string) {
	if d.connected {
		d.sendQueue <- fmt.Sprintf("%s:%s", user, message)
	}
}

func (d *DirectMode) directReadLoop(ctx context.Context) {
	defer d.wg.Done()
	buf := make([]byte, 2048)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			d.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, err := d.conn.Read(buf)
			if err == nil && n > 0 {
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
					if d.OnMessageReceived != nil {
						d.OnMessageReceived(user, message)
					}
				} else {
					if d.OnMessageReceived != nil {
						d.OnMessageReceived("Peer", msg)
					}
				}
			}
		}
	}
}

func (d *DirectMode) directWriteLoop(ctx context.Context) {
	defer d.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-d.sendQueue:
			d.conn.Write([]byte(msg))
		}
	}
}

func (d *DirectMode) Connected() bool {
	return d.connected
}

func (d *DirectMode) Shutdown() {
	if d.listener != nil {
		d.listener.Close()
		d.listener = nil
	}
	if d.conn != nil {
		d.conn.Close()
		d.conn = nil
	}
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}
	d.connected = false
	d.wg.Wait()
}