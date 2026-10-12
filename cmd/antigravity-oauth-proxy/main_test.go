package main

import "testing"

func TestListenAddressUsesLoopback(t *testing.T) {
	if got, want := listenAddress("9878"), "127.0.0.1:9878"; got != want {
		t.Fatalf("listenAddress(9878) = %q, want %q", got, want)
	}
}
