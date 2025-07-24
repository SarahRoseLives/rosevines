package main

import (
	"encoding/json"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

type Peer struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	PubKey    string    `json:"pubkey"`
	Address   string    `json:"address"`
	LastSeen  time.Time `json:"last_seen"`
}

type Server struct {
	mu    sync.Mutex
	peers map[string]*Peer // map of ID to Peer
}

func NewServer() *Server {
	return &Server{
		peers: make(map[string]*Peer),
	}
}

func (s *Server) RegisterPeer(w http.ResponseWriter, r *http.Request) {
	var peer Peer
	if err := json.NewDecoder(r.Body).Decode(&peer); err != nil {
		log.Printf("[RegisterPeer] Invalid peer registration from %s: %v", r.RemoteAddr, err)
		http.Error(w, "invalid peer", http.StatusBadRequest)
		return
	}
	// If peer.Address is blank, try to fill it with r.RemoteAddr (for local testing)
	if peer.Address == "" {
		remoteIP, _, err := net.SplitHostPort(r.RemoteAddr)
		if err == nil {
			peer.Address = remoteIP + ":9312"
			log.Printf("[RegisterPeer] Peer address not provided, using %s", peer.Address)
		}
	}
	peer.LastSeen = time.Now()
	s.mu.Lock()
	s.peers[peer.ID] = &peer
	s.mu.Unlock()
	log.Printf("[RegisterPeer] Registered peer: ID=%s Username=%s Address=%s", peer.ID, peer.Username, peer.Address)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) Heartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Printf("[Heartbeat] Invalid heartbeat from %s: %v", r.RemoteAddr, err)
		http.Error(w, "invalid heartbeat", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if peer, ok := s.peers[req.ID]; ok {
		peer.LastSeen = time.Now()
		log.Printf("[Heartbeat] Heartbeat received for peer ID=%s", req.ID)
	} else {
		log.Printf("[Heartbeat] Heartbeat for unknown peer ID=%s", req.ID)
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

func (s *Server) ListPeers(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Only return peers seen in the last 2 minutes
	cutoff := time.Now().Add(-2 * time.Minute)
	var active []*Peer
	for _, p := range s.peers {
		if p.LastSeen.After(cutoff) {
			active = append(active, p)
		}
	}
	log.Printf("[ListPeers] Returned %d active peers to %s", len(active), r.RemoteAddr)
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(active); err != nil {
		log.Printf("[ListPeers] Failed to encode peers: %v", err)
	}
}

func main() {
	server := NewServer()
	http.HandleFunc("/register", server.RegisterPeer)
	http.HandleFunc("/heartbeat", server.Heartbeat)
	http.HandleFunc("/peers", server.ListPeers)

	log.Println("Bootstrap server started on :9000")
	log.Fatal(http.ListenAndServe(":9000", nil))
}