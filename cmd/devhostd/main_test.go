package main

import (
	"reflect"
	"testing"
)

func TestBackgroundDaemonArgsRemovesForeground(t *testing.T) {
	input := []string{"daemon", "start", "--foreground", "--port", "443", "--tld", "localhost"}
	want := []string{"daemon", "start", "--port", "443", "--tld", "localhost"}
	if got := backgroundDaemonArgs(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("backgroundDaemonArgs()=%v, want %v", got, want)
	}
	if !reflect.DeepEqual(input, []string{"daemon", "start", "--foreground", "--port", "443", "--tld", "localhost"}) {
		t.Fatal("backgroundDaemonArgs modified its input")
	}
}
