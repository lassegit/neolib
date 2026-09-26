package main

import "testing"

func TestListenURL(t *testing.T) {
	tests := []struct {
		addr string
		want string
	}{
		{":3000", "http://localhost:3000"},
		{"0.0.0.0:3000", "http://localhost:3000"},
		{"[::]:3000", "http://localhost:3000"},
		{"127.0.0.1:8080", "http://127.0.0.1:8080"},
		{"[::1]:3000", "http://[::1]:3000"},
		{"bookbox.local:3000", "http://bookbox.local:3000"},
		{"nonsense", "http://nonsense"},
	}
	for _, tt := range tests {
		if got := listenURL(tt.addr); got != tt.want {
			t.Errorf("listenURL(%q) = %q, want %q", tt.addr, got, tt.want)
		}
	}
}
