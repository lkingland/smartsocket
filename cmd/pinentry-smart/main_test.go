package main

import (
	"bytes"
	"io"
	"net"
	"path/filepath"
	"strings"
	"testing"
)

// startFakePinentry serves a unix socket that optionally sends an Assuan greeting
// and then echoes the dialogue (so the relay can be observed end-to-end).
func startFakePinentry(t *testing.T, greeting string, echo bool) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "pe.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				if greeting != "" {
					io.WriteString(c, greeting)
				}
				if echo {
					io.Copy(c, c) // echo gpg-agent's commands back until EOF
				}
			}(c)
		}
	}()
	return sock
}

func TestForwardLiveRemote(t *testing.T) {
	sock := startFakePinentry(t, "OK Pleased to meet you\n", true)
	out := &bytes.Buffer{}
	if !forward(strings.NewReader("GETPIN\n"), out, sock) {
		t.Fatal("live remote: want true, got false")
	}
	got := out.String()
	if !strings.HasPrefix(got, "OK ") {
		t.Errorf("greeting not relayed first: %q", got)
	}
	if !strings.Contains(got, "GETPIN") {
		t.Errorf("dialogue not piped through: %q", got)
	}
}

func TestForwardDeadResponder(t *testing.T) {
	// socket present, but the responder sends no greeting and closes — must fail
	// closed (return false) so the caller falls back, WITHOUT having consumed in.
	sock := startFakePinentry(t, "", false)
	in := strings.NewReader("SHOULD-NOT-BE-READ\n")
	if forward(in, &bytes.Buffer{}, sock) {
		t.Error("dead responder: want false, got true")
	}
	if n, _ := io.Copy(io.Discard, in); n == 0 {
		t.Error("input was consumed on the fail-closed path; fallback would get a truncated dialogue")
	}
}

func TestForwardNonOKGreeting(t *testing.T) {
	// a connectable endpoint that isn't a pinentry (no OK greeting) → false.
	sock := startFakePinentry(t, "garbage not assuan\n", false)
	if forward(strings.NewReader("x"), &bytes.Buffer{}, sock) {
		t.Error("non-OK greeting: want false, got true")
	}
}

func TestForwardNoSocket(t *testing.T) {
	if forward(strings.NewReader("x"), &bytes.Buffer{}, filepath.Join(t.TempDir(), "nope.sock")) {
		t.Error("missing socket: want false, got true")
	}
}
