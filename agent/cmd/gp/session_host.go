package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type localSessionInputRequest struct {
	Text string `json:"text"`
}

type localSessionHost struct {
	sessionID string
	addr      string
	server    *http.Server
	writer    io.Writer
	mu        sync.Mutex
}

func startLocalSessionHost(sessionID string, writer io.Writer) (*localSessionHost, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	host := &localSessionHost{sessionID: sessionID, addr: listener.Addr().String(), writer: writer}
	mux := http.NewServeMux()
	mux.HandleFunc("/input", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req localSessionInputRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(req.Text) == "" {
			http.Error(w, "text is required", http.StatusBadRequest)
			return
		}
		host.mu.Lock()
		n, err := host.writer.Write([]byte(req.Text + "\r"))
		host.mu.Unlock()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = appendLocalDecision(localDecisionRecord{
			ApprovalID:      "manual_input",
			SessionID:       sessionID,
			DecisionType:    "reply",
			PayloadRedacted: req.Text,
			BytesWritten:    n,
			Result:          "manual_input",
			CreatedAt:       time.Now().UTC().Format(time.RFC3339),
		})
		writeJSON(w, map[string]any{"data": map[string]any{"bytes_written": n}})
	})
	host.server = &http.Server{Handler: mux}
	go func() {
		if err := host.server.Serve(listener); err != nil && err != http.ErrServerClosed {
			_ = err
		}
	}()
	return host, nil
}

func (h *localSessionHost) Close() error {
	if h == nil || h.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return h.server.Shutdown(ctx)
}

func localHostAddress(host *localSessionHost) string {
	if host == nil {
		return ""
	}
	return host.addr
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		http.Error(w, fmt.Sprintf("encode response: %v", err), http.StatusInternalServerError)
	}
}
