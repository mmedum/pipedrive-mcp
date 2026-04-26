package server

import "testing"

func TestNew_ReturnsServer(t *testing.T) {
	srv := New("pipedrive-mcp", "test", nil, "")
	if srv == nil {
		t.Fatal("New returned nil server")
	}
}
