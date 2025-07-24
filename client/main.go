package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"

	"rosevines-client/modes"
)

type chatMsg struct {
	timestamp time.Time
	user      string
	message   string
}

type directMsg struct {
	user    string
	message string
}

type directStatusMsg struct {
	status    string
	connected bool
}

type mdnsMsg struct {
	user    string
	message string
}

type bootstrapMsg struct {
	user    string
	message string
}

type Mode int

const (
	ModeConfig Mode = iota
	ModeChat
)

type ConnectionMode int

const (
	ConnNone ConnectionMode = iota
	ConnMDNS
	ConnDirect
	ConnDHT
	ConnBootstrap
)

var whiteStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("15"))

type model struct {
	messages         []chatMsg
	textInput        textinput.Model
	username         string
	configuredNick   bool
	configuredMode   bool
	configMode       Mode
	connectionMode   ConnectionMode
	connAddr         string
	connPort         int
	connDirectMode   string // "server" or "client"
	connected        bool
	directStatus     string
	mdnsFindingPeers bool
	userCount        int
	width            int
	height           int
	scroll           int
	shouldQuit       bool
	infoMessages     []string
	mdns             *modes.MDNSMode
	direct           *modes.DirectMode
	bootstrap        *modes.BootstrapMode
	bootstrapPeers   []modes.Peer
	networkMsgs      chan tea.Msg
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "Type /nick <username> to set your nickname"
	ti.Focus()
	ti.CharLimit = 140
	ti.Width = 50

	return model{
		messages:         nil,
		textInput:        ti,
		username:         "",
		configuredNick:   false,
		configuredMode:   false,
		configMode:       ModeConfig,
		connectionMode:   ConnNone,
		connAddr:         "",
		connPort:         modes.DefaultPort,
		connDirectMode:   "",
		connected:        false,
		directStatus:     "",
		mdnsFindingPeers: false,
		userCount:        1,
		width:            80,
		height:           24,
		scroll:           0,
		shouldQuit:       false,
		infoMessages: []string{
			"Welcome to RoseVines!",
			"Set your nickname and mode to get started.",
			"Available commands:",
			"  /nick <username>                                - Set your nickname",
			"  /mode mdns                                      - LAN chat (auto-discover peers)",
			"  /mode direct server [port]                      - Direct mode, listen for peer (optional port, default 9312)",
			"  /mode direct client <ip>[:port]                 - Direct mode, connect to peer (port optional, default 9312)",
			"  /mode bootstrap <host:port>                     - Use bootstrap server for peer discovery",
			"  /mode dht                                       - (future) Distributed Hash Table",
			"  /quit                                           - Exit",
			"",
			"Examples:",
			"  /nick SarahRoseLives",
			"  /mode direct server 9312",
			"  /mode direct client 127.0.0.1:9312",
		},
		mdns:        nil,
		direct:      nil,
		bootstrap:   nil,
		networkMsgs: make(chan tea.Msg, 32),
	}
}

func (m *model) shutdownMDNS() {
	if m.mdns != nil {
		m.mdns.Shutdown()
		m.mdns = nil
	}
}

func (m *model) shutdownDirect() {
	if m.direct != nil {
		m.direct.Shutdown()
		m.direct = nil
	}
}

func (m *model) shutdownBootstrap() {
	if m.bootstrap != nil {
		m.bootstrap.Shutdown()
		m.bootstrap = nil
	}
}

func (m *model) startMDNS() {
	m.shutdownMDNS()
	mdns := modes.NewMDNSMode(m.username, m.connPort)
	mdns.OnPeerDiscovered = func(name, addr string, count int) {
		m.networkMsgs <- mdnsMsg{
			user:    "SYSTEM",
			message: fmt.Sprintf("Discovered mDNS user: %s %s", name, addr),
		}
	}
	mdns.OnMessageReceived = func(user, message string) {
		m.networkMsgs <- mdnsMsg{
			user:    user,
			message: message,
		}
	}
	mdns.OnPeerCountChanged = func(count int) {
		m.userCount = 1 + count
		if count > 0 {
			m.connected = true
			m.mdnsFindingPeers = false
		} else {
			m.connected = false
			m.mdnsFindingPeers = true
		}
	}
	mdns.Start()
	m.mdns = mdns
	m.mdnsFindingPeers = true
	m.connected = false
	m.networkMsgs <- mdnsMsg{
		user:    "SYSTEM",
		message: "mDNS started (LAN mode). Finding Peers...",
	}
}

func (m *model) startDirect(addr string, port int, directMode string) {
	m.shutdownDirect()
	direct := modes.NewDirectMode(m.username, addr, port, directMode)
	direct.OnMessageReceived = func(user, message string) {
		m.networkMsgs <- directMsg{user: user, message: message}
	}
	direct.OnStatus = func(msg string) {
		m.networkMsgs <- directStatusMsg{status: msg, connected: direct.Connected()}
	}
	direct.Start()
	m.direct = direct
	m.connected = direct.Connected()
}

func (m *model) startBootstrap(serverAddr string) {
	m.shutdownBootstrap()
	bootstrap := modes.NewBootstrapMode(m.username, serverAddr)
	bootstrap.ListenPort = m.connPort // Pass chosen port
	bootstrap.OnPeerListReceived = func(peers []modes.Peer) {
		m.bootstrapPeers = peers
		m.userCount = len(peers)
		m.connected = len(peers) > 1
	}
	bootstrap.OnMessageReceived = func(user, message string) {
		m.networkMsgs <- bootstrapMsg{user: user, message: message}
	}
	bootstrap.Start()
	m.bootstrap = bootstrap
	m.connected = false
	m.networkMsgs <- bootstrapMsg{
		user:    "SYSTEM",
		message: "Connected to bootstrap server. Polling for peers...",
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(
		textinput.Blink,
		m.listenNetworkMsgs(),
	)
}

func (m *model) listenNetworkMsgs() tea.Cmd {
	return func() tea.Msg {
		return <-m.networkMsgs
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case directMsg:
		m.messages = append(m.messages, chatMsg{
			timestamp: time.Now(),
			user:      msg.user,
			message:   msg.message,
		})
		m.scroll = len(m.messages)
		return m, m.listenNetworkMsgs()
	case directStatusMsg:
		m.directStatus = msg.status
		m.connected = msg.connected
		if m.connectionMode == ConnDirect {
			if msg.connected {
				m.userCount = 2
			} else {
				m.userCount = 1
			}
		}
		return m, m.listenNetworkMsgs()
	case mdnsMsg:
		m.messages = append(m.messages, chatMsg{
			timestamp: time.Now(),
			user:      msg.user,
			message:   msg.message,
		})
		m.scroll = len(m.messages)
		return m, m.listenNetworkMsgs()
	case bootstrapMsg:
		m.messages = append(m.messages, chatMsg{
			timestamp: time.Now(),
			user:      msg.user,
			message:   msg.message,
		})
		m.scroll = len(m.messages)
		return m, m.listenNetworkMsgs()
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			input := strings.TrimSpace(m.textInput.Value())
			m.textInput.SetValue("")

			if m.configMode == ModeConfig {
				if strings.HasPrefix(input, "/nick ") {
					nick := strings.TrimSpace(strings.TrimPrefix(input, "/nick"))
					if nick != "" {
						m.username = nick
						m.configuredNick = true
						m.infoMessages = append(m.infoMessages, fmt.Sprintf("Nickname set to %s", nick))
						m.textInput.Placeholder = "Type /mode <mdns|direct|bootstrap|dht> to set mode"
					} else {
						m.infoMessages = append(m.infoMessages, "Nickname cannot be empty.")
					}
				} else if strings.HasPrefix(input, "/mode ") {
					modeArg := strings.TrimSpace(strings.TrimPrefix(input, "/mode"))
					switch {
					case modeArg == "mdns":
						m.connectionMode = ConnMDNS
						m.configuredMode = true
						m.infoMessages = append(m.infoMessages, "Mode set to mDNS (LAN chat)")
					case strings.HasPrefix(modeArg, "direct"):
						rest := strings.TrimSpace(strings.TrimPrefix(modeArg, "direct"))
						parts := strings.Fields(rest)
						if len(parts) < 1 {
							m.infoMessages = append(m.infoMessages, "Usage: /mode direct server [port] OR /mode direct client <ip>[:port]")
							break
						}
						mode := parts[0]
						addr := ""
						port := modes.DefaultPort
						if mode == "server" {
							if len(parts) > 1 {
								p, err := strconv.Atoi(parts[1])
								if err == nil {
									port = p
								}
							}
							m.connectionMode = ConnDirect
							m.connAddr = ""
							m.connPort = port
							m.connDirectMode = "server"
							m.configuredMode = true
							m.infoMessages = append(m.infoMessages, fmt.Sprintf("Mode set to Direct Server. Listening on port %d", port))
						} else if mode == "client" {
							if len(parts) < 2 {
								m.infoMessages = append(m.infoMessages, "Direct client mode requires address: /mode direct client <ip>[:port]")
								break
							}
							addrPort := parts[1]
							addrParts := strings.Split(addrPort, ":")
							if len(addrParts) == 2 {
								addr = addrParts[0]
								p, err := strconv.Atoi(addrParts[1])
								if err == nil {
									port = p
								}
							} else {
								addr = addrPort
							}
							m.connectionMode = ConnDirect
							m.connAddr = addr
							m.connPort = port
							m.connDirectMode = "client"
							m.configuredMode = true
							m.infoMessages = append(m.infoMessages, fmt.Sprintf("Mode set to Direct Client. Connecting to %s:%d", addr, port))
						} else {
							m.infoMessages = append(m.infoMessages, "Direct mode: first argument must be 'server' or 'client'.")
						}
					case strings.HasPrefix(modeArg, "bootstrap"):
						addr := strings.TrimSpace(strings.TrimPrefix(modeArg, "bootstrap"))
						if addr == "" {
							m.infoMessages = append(m.infoMessages, "Bootstrap mode requires a server address: /mode bootstrap <host:port>")
						} else {
							m.connectionMode = ConnBootstrap
							m.connAddr = addr
							m.configuredMode = true
							m.infoMessages = append(m.infoMessages, fmt.Sprintf("Mode set to Bootstrap. Server: %s", addr))
						}
					case modeArg == "dht":
						m.connectionMode = ConnDHT
						m.configuredMode = true
						m.infoMessages = append(m.infoMessages, "Mode set to DHT (future feature)")
					default:
						m.infoMessages = append(m.infoMessages, "Unknown mode. Use /mode mdns, /mode direct server/client, /mode bootstrap <host:port>, or /mode dht")
					}
				} else if input == "/quit" {
					m.shouldQuit = true
					m.shutdownMDNS()
					m.shutdownDirect()
					m.shutdownBootstrap()
					return m, tea.Quit
				} else if input != "" {
					m.infoMessages = append(m.infoMessages, "Unknown command. See above for available commands.")
				}
				if m.configuredNick && m.configuredMode {
					m.configMode = ModeChat
					m.connected = false
					m.mdnsFindingPeers = false
					m.messages = append(m.messages, chatMsg{
						timestamp: time.Now(),
						user:      "SYSTEM",
						message:   fmt.Sprintf("Welcome %s! Connected in mode: %s", m.username, m.modeName()),
					})
					m.textInput.Placeholder = "Type your message and press Enter"
					switch m.connectionMode {
					case ConnMDNS:
						m.startMDNS()
					case ConnDirect:
						m.startDirect(m.connAddr, m.connPort, m.connDirectMode)
					case ConnBootstrap:
						m.startBootstrap(m.connAddr)
					}
				}
			} else if m.configMode == ModeChat {
				if input == "/quit" {
					m.shouldQuit = true
					m.shutdownMDNS()
					m.shutdownDirect()
					m.shutdownBootstrap()
					return m, tea.Quit
				} else if strings.HasPrefix(input, "/nick ") {
					nick := strings.TrimSpace(strings.TrimPrefix(input, "/nick"))
					if nick != "" {
						m.username = nick
						m.messages = append(m.messages, chatMsg{
							timestamp: time.Now(),
							user:      "SYSTEM",
							message:   fmt.Sprintf("Nickname changed to %s", nick),
						})
					}
				} else if input != "" {
					m.messages = append(m.messages, chatMsg{
						timestamp: time.Now(),
						user:      m.username,
						message:   input,
					})
					m.scroll = len(m.messages)
					switch m.connectionMode {
					case ConnDirect:
						if m.direct != nil && m.direct.Connected() {
							m.direct.SendMessage(m.username, input)
						}
					case ConnMDNS:
						if m.mdns != nil {
							m.mdns.Broadcast(m.username, input)
						}
					case ConnBootstrap:
						if m.bootstrap != nil {
							m.bootstrap.Broadcast(m.username, input)
						}
					}
				}
			}
		case tea.KeyUp:
			if m.configMode == ModeChat && m.scroll > 0 {
				m.scroll--
			}
		case tea.KeyDown:
			if m.configMode == ModeChat && m.scroll < len(m.messages) {
				m.scroll++
			}
		}
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.textInput.Width != m.width-4 {
			m.textInput.Width = m.width - 4
		}
	}
	var cmd tea.Cmd
	m.textInput, cmd = m.textInput.Update(msg)
	return m, cmd
}

func padOrCrop(s string, width int) string {
	if len(s) < width {
		return s + strings.Repeat(" ", width-len(s))
	}
	if len(s) > width {
		return s[:width]
	}
	return s
}

func (m model) modeName() string {
	switch m.connectionMode {
	case ConnMDNS:
		return "mDNS"
	case ConnDirect:
		if m.connDirectMode != "" {
			return "Direct (" + m.connDirectMode + ")"
		}
		return "Direct"
	case ConnBootstrap:
		return "Bootstrap"
	case ConnDHT:
		return "DHT"
	default:
		return "None"
	}
}

func (m model) View() string {
	if m.shouldQuit {
		return whiteStyle.Render(padOrCrop("Exiting RoseVines. Goodbye!", m.width))
	}
	if m.configMode == ModeConfig {
		s := ""
		for _, line := range m.infoMessages {
			s += padOrCrop(line, m.width) + "\n"
		}
		s += padOrCrop(strings.Repeat("-", m.width), m.width) + "\n"
		s += padOrCrop(m.textInput.View(), m.width) + "\n"
		s += padOrCrop(strings.Repeat("-", m.width), m.width) + "\n"
		return whiteStyle.Render(s)
	}
	headerLines := 3
	footerLines := 3
	inputLines := 2

	chatHeight := m.height - headerLines - footerLines - inputLines
	if chatHeight < 3 {
		chatHeight = 3
	}

	s := ""
	connectionStatus := ""
	switch m.connectionMode {
	case ConnMDNS:
		if m.mdnsFindingPeers {
			connectionStatus = "Finding Peers..."
		} else if m.connected {
			connectionStatus = "Connected"
		} else {
			connectionStatus = "Not Connected"
		}
	case ConnDirect:
		if m.directStatus != "" {
			connectionStatus = m.directStatus
		} else if m.connected {
			connectionStatus = "Connected"
		} else if m.connDirectMode == "server" {
			connectionStatus = "Waiting for Peer..."
		} else {
			connectionStatus = "Connecting..."
		}
	case ConnBootstrap:
		if m.connected {
			connectionStatus = fmt.Sprintf("Peers: %d", m.userCount)
		} else {
			connectionStatus = "Polling server..."
		}
	default:
		connectionStatus = "N/A"
	}

	s += padOrCrop(fmt.Sprintf(" RoseVines Chat        Username: %s        Mode: %s        Status: %s", m.username, m.modeName(), connectionStatus), m.width) + "\n"
	s += padOrCrop(strings.Repeat("-", m.width), m.width) + "\n"

	total := len(m.messages)
	start := 0
	if total > chatHeight {
		start = total - chatHeight
		if m.scroll < total-chatHeight {
			start = m.scroll
		}
	}
	end := start + chatHeight
	if end > total {
		end = total
	}
	visible := m.messages[start:end]

	for _, msg := range visible {
		timeStr := fmt.Sprintf("[%02d:%02d]", msg.timestamp.Hour(), msg.timestamp.Minute())
		line := fmt.Sprintf(" %s %s: %s", timeStr, msg.user, msg.message)
		s += padOrCrop(line, m.width) + "\n"
	}
	for i := len(visible); i < chatHeight; i++ {
		s += padOrCrop("", m.width) + "\n"
	}

	s += padOrCrop(strings.Repeat("-", m.width), m.width) + "\n"
	s += padOrCrop(m.textInput.View(), m.width) + "\n"
	s += padOrCrop(strings.Repeat("-", m.width), m.width) + "\n"
	status := fmt.Sprintf(" Users: %d | /nick <username> to change nickname | /quit to exit", m.userCount)
	s += padOrCrop(status, m.width) + "\n"

	return whiteStyle.Render(s)
}

func main() {
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if err := p.Start(); err != nil {
		fmt.Println("Error running program:", err)
		os.Exit(1)
	}
}