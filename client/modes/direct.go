package modes

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type DirectMode struct {
	username          string
	addr              string
	port              int
	mode              string // "server" or "client"
	listener          net.Listener
	sshConn           ssh.Conn    // The underlying SSH connection
	sshChannel        ssh.Channel // The channel for communication
	connected         bool
	cancel            context.CancelFunc
	OnMessageReceived func(user, message string)
	OnStatus          func(msg string)
	sendQueue         chan string
	wg                sync.WaitGroup
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

// Client mode: connect to a peer via SSH
func (d *DirectMode) runClient() {
	target := fmt.Sprintf("%s:%d", d.addr, d.port)
	if d.OnStatus != nil {
		d.OnStatus(fmt.Sprintf("SSH: Trying to connect to peer at %s...", target))
	}

	// For this simple chat, we'll trust any host key.
	// In a real-world application, you would want to validate the host key
	// to prevent man-in-the-middle attacks.
	config := &ssh.ClientConfig{
		User: d.username,
		Auth: []ssh.AuthMethod{
			// The server is configured with NoClientAuth, so any password will do.
			ssh.Password("dummy"),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	tcpConn, err := net.DialTimeout("tcp", target, 10*time.Second)
	if err != nil {
		if d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("Failed to connect to peer at %s: %v", target, err))
		}
		return
	}

	sshClientConn, _, reqs, err := ssh.NewClientConn(tcpConn, target, config)
	if err != nil {
		if d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("SSH handshake failed: %v", err))
		}
		tcpConn.Close()
		return
	}
	d.sshConn = sshClientConn
	go ssh.DiscardRequests(reqs) // We don't expect any out-of-band requests

	// Open a "session" channel for communication
	channel, sessionReqs, err := d.sshConn.OpenChannel("session", nil)
	if err != nil {
		if d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("Could not open session: %v", err))
		}
		d.Shutdown()
		return
	}
	go ssh.DiscardRequests(sessionReqs)
	d.sshChannel = channel

	d.connected = true
	if d.OnStatus != nil {
		d.OnStatus(fmt.Sprintf("SSH connection established with %s", target))
	}

	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.wg.Add(2)
	go d.directReadLoop(ctx)
	go d.directWriteLoop(ctx)
}

// Server mode: listen for an SSH connection
func (d *DirectMode) runServer() {
	listenAddr := fmt.Sprintf("0.0.0.0:%d", d.port)
	if d.OnStatus != nil {
		d.OnStatus(fmt.Sprintf("Listening for a direct SSH peer on port %d...", d.port))
	}

	config := &ssh.ServerConfig{
		// NoClientAuth allows any client to connect without authentication.
		NoClientAuth: true,
	}

	// Generate a temporary host key. In a real app, load this from a file.
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		if d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("Failed to generate private key: %v", err))
		}
		return
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		if d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("Failed to create signer: %v", err))
		}
		return
	}
	config.AddHostKey(signer)

	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		if d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("Failed to listen on port %d: %v", d.port, err))
		}
		return
	}
	d.listener = ln

	tcpConn, err := ln.Accept()
	if err != nil {
		// This can happen during shutdown, so check if the listener is still active.
		if d.listener != nil && d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("Accept error: %v", err))
		}
		return
	}

	// Perform the SSH handshake
	sshServerConn, chans, reqs, err := ssh.NewServerConn(tcpConn, config)
	if err != nil {
		log.Printf("SSH handshake failed: %v", err)
		if d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("SSH handshake failed: %v", err))
		}
		return
	}
	d.sshConn = sshServerConn
	go ssh.DiscardRequests(reqs)

	// Wait for the client to open a "session" channel
	newChannel := <-chans
	if newChannel == nil {
		if d.OnStatus != nil {
			d.OnStatus("Client did not open a session channel.")
		}
		d.Shutdown()
		return
	}
	if newChannel.ChannelType() != "session" {
		newChannel.Reject(ssh.UnknownChannelType, "unknown channel type")
		return
	}

	channel, sessionReqs, err := newChannel.Accept()
	if err != nil {
		if d.OnStatus != nil {
			d.OnStatus(fmt.Sprintf("Could not accept channel: %v", err))
		}
		d.Shutdown()
		return
	}
	d.sshChannel = channel
	go ssh.DiscardRequests(sessionReqs)

	d.connected = true
	if d.OnStatus != nil {
		d.OnStatus(fmt.Sprintf("Peer %s connected from %s", sshServerConn.User(), sshServerConn.RemoteAddr().String()))
	}

	ctx, cancel := context.WithCancel(context.Background())
	d.cancel = cancel
	d.wg.Add(2)
	go d.directReadLoop(ctx)
	go d.directWriteLoop(ctx)
}

func (d *DirectMode) SendMessage(user, message string) {
	if d.connected {
		// Add a newline to delimit messages for the buffered reader.
		d.sendQueue <- fmt.Sprintf("%s:%s\n", user, message)
	}
}

func (d *DirectMode) directReadLoop(ctx context.Context) {
	defer d.wg.Done()
	defer d.handleDisconnect()

	reader := bufio.NewReader(d.sshChannel)
	for {
		select {
		case <-ctx.Done():
			return
		default:
			msg, err := reader.ReadString('\n')
			if err != nil {
				// io.EOF means the peer disconnected gracefully. Other errors are treated as disconnects.
				return
			}

			// Trim newline before processing
			msg = msg[:len(msg)-1]

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
			if _, err := d.sshChannel.Write([]byte(msg)); err != nil {
				// Error on write likely means a disconnect. The read loop will detect and handle it.
				return
			}
		}
	}
}

// handleDisconnect ensures the state is cleaned up and the UI is notified.
func (d *DirectMode) handleDisconnect() {
	// Use a mutex or check `d.connected` if you expect concurrent calls, but here it's fine.
	if d.connected {
		d.connected = false
		if d.OnStatus != nil {
			d.OnStatus("Peer disconnected.")
		}
		// This will trigger the shutdown of the read/write loops via context cancellation.
		d.Shutdown()
	}
}

func (d *DirectMode) Connected() bool {
	return d.connected
}

func (d *DirectMode) Shutdown() {
	if d.cancel != nil {
		d.cancel()
		d.cancel = nil
	}

	if d.sshChannel != nil {
		d.sshChannel.Close()
		d.sshChannel = nil
	}
	if d.sshConn != nil {
		d.sshConn.Close()
		d.sshConn = nil
	}
	if d.listener != nil {
		d.listener.Close()
		d.listener = nil
	}

	d.connected = false
	d.wg.Wait()
}