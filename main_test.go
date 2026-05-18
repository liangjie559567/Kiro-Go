package main

import (
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPServerSetsReadHeaderTimeout(t *testing.T) {
	server := newHTTPServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if server.Addr != "127.0.0.1:0" {
		t.Fatalf("unexpected addr %q", server.Addr)
	}
	if server.Handler == nil {
		t.Fatalf("expected handler")
	}
	if server.ReadHeaderTimeout != 10*time.Second {
		t.Fatalf("expected read header timeout, got %s", server.ReadHeaderTimeout)
	}
}
