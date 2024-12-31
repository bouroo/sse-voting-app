package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"syscall"
	"time"
)

// Candidate structure to hold candidate data
type Candidate struct {
	Name  string `json:"name"`
	Votes int    `json:"votes"`
}

// VoteManager manages votes and client notifications
type VoteManager struct {
	candidates  map[string]*Candidate
	voteChannel chan string
	clients     map[chan string]struct{}
	cliRequests chan cliRequest
	wg          sync.WaitGroup
}

// cliRequest represents a request to modify the clients
type cliRequest struct {
	clientChan chan string
	action     string // "add" or "remove"
}

// NewVoteManager initializes and returns a VoteManager
func NewVoteManager() *VoteManager {
	vm := &VoteManager{
		candidates: map[string]*Candidate{
			"Candidate A": {Name: "Candidate A", Votes: 0},
			"Candidate B": {Name: "Candidate B", Votes: 0},
		},
		voteChannel: make(chan string, runtime.NumCPU()*2), // Buffered channel for votes
		clients:     make(map[chan string]struct{}),
		cliRequests: make(chan cliRequest), // Channel for client management
	}
	go vm.manageClients() // Start the client management goroutine
	return vm
}

// Start begins processing votes
func (vm *VoteManager) Start(ctx context.Context) {
	vm.wg.Add(1)
	go func() {
		defer vm.wg.Done()
		for {
			select {
			case candidateName, ok := <-vm.voteChannel:
				if !ok {
					return
				}
				vm.processVote(candidateName)
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (vm *VoteManager) processVote(candidateName string) {
	if candidate, exists := vm.candidates[candidateName]; exists {
		candidate.Votes++
		vm.notifyClients(candidate)
	} else {
		log.Printf("Received vote for unknown candidate: %s", candidateName)
	}
}

// manageClients handles adding and removing client channels
func (vm *VoteManager) manageClients() {
	for req := range vm.cliRequests {
		if req.action == "add" {
			vm.clients[req.clientChan] = struct{}{}
		} else if req.action == "remove" {
			close(req.clientChan)
			delete(vm.clients, req.clientChan)
		}
	}
}

// Stop gracefully stops the VoteManager
func (vm *VoteManager) Stop() {
	close(vm.voteChannel)
	vm.wg.Wait()

	// Close all client channels
	for clientChan := range vm.clients {
		close(clientChan)
		delete(vm.clients, clientChan)
	}
}

// notifyClients sends updated candidate data to all connected clients
func (vm *VoteManager) notifyClients(candidate *Candidate) {
	message, err := json.Marshal(candidate)
	if err != nil {
		log.Printf("Failed to marshal candidate: %v", err)
		return
	}

	for clientChan := range vm.clients {
		select {
		case clientChan <- string(message):
		default:
			log.Println("Skipping sending to a slow client")
		}
	}
}

// AddClient registers a new client channel
func (vm *VoteManager) AddClient(clientChan chan string) {
	vm.cliRequests <- cliRequest{clientChan: clientChan, action: "add"}
}

// RemoveClient unregisters a client channel
func (vm *VoteManager) RemoveClient(clientChan chan string) {
	vm.cliRequests <- cliRequest{clientChan: clientChan, action: "remove"}
}

func main() {
	// Initialize VoteManager
	vm := NewVoteManager()

	// Create a context that is canceled on shutdown
	ctx, cancel := context.WithCancel(context.Background())

	// Start VoteManager
	vm.Start(ctx)

	// Handle graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)

	// Create HTTP server with context
	mux := http.NewServeMux()
	mux.Handle("/vote", corsMiddleware(http.HandlerFunc(vm.voteHandler)))
	mux.Handle("/results", corsMiddleware(http.HandlerFunc(vm.resultsHandler)))
	mux.Handle("/events", corsMiddleware(http.HandlerFunc(vm.sseHandler)))

	srv := &http.Server{
		Addr:        ":8080",
		Handler:     mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start server in a goroutine
	go func() {
		log.Println("Server started on :8080")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("ListenAndServe(): %v", err)
		}
	}()

	// Wait for shutdown signal
	<-quit
	log.Println("Shutdown signal received")

	// Initiate shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server Shutdown Failed:%+v", err)
	}

	// Stop VoteManager
	cancel()
	vm.Stop()

	log.Println("Server gracefully stopped")
}

// voteHandler accepts votes for candidates
func (vm *VoteManager) voteHandler(w http.ResponseWriter, r *http.Request) {
	candidateName := r.URL.Query().Get("candidate")
	if candidateName == "" {
		http.Error(w, "Candidate name is required", http.StatusBadRequest)
		return
	}
	select {
	case vm.voteChannel <- candidateName:
		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "Server is busy, try again later", http.StatusServiceUnavailable)
	}
}

// resultsHandler returns the current voting results
func (vm *VoteManager) resultsHandler(w http.ResponseWriter, r *http.Request) {
	candidateList := make([]*Candidate, 0, len(vm.candidates))
	for _, candidate := range vm.candidates {
		c := &Candidate{
			Name:  candidate.Name,
			Votes: candidate.Votes,
		}
		candidateList = append(candidateList, c)
	}
	if err := json.NewEncoder(w).Encode(candidateList); err != nil {
		http.Error(w, "Failed to encode results", http.StatusInternalServerError)
	}
}

// sseHandler handles Server-Sent Events (SSE) for real-time updates
func (vm *VoteManager) sseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	clientChan := make(chan string, runtime.NumCPU()*2) // Buffered to prevent blocking
	vm.AddClient(clientChan)
	defer vm.RemoveClient(clientChan)

	// Send initial data
	initialData, err := json.Marshal(vm.candidates)
	if err == nil {
		w.Write([]byte("data: " + string(initialData) + "\n\n"))
		flusher.Flush()
	}

	notify := r.Context().Done()

	pingTicker := time.NewTicker(1 * time.Minute)
	defer pingTicker.Stop()

	for {
		select {
		case msg, ok := <-clientChan:
			if !ok {
				return
			}
			if _, err := w.Write([]byte("data: " + msg + "\n\n")); err != nil {
				log.Println("Error writing to client:", err)
				return
			}
			flusher.Flush()

		case <-notify:
			return

		case <-pingTicker.C:
			if _, err := w.Write([]byte(":\n\n")); err != nil {
				log.Println("Error during ping:", err)
				return
			}
			flusher.Flush()
		}
	}
}

// corsMiddleware adds CORS headers to responses
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
