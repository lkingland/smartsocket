package main

import (
	"bufio"
	"encoding/binary"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

// startFakeSSHAgentRaw serves a unix socket that reads the (5-byte)
// REQUEST_IDENTITIES request and replies with raw bytes — for crafting both
// well-formed and malformed answers.
func startFakeSSHAgentRaw(t *testing.T, reply []byte) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "ssh.sock")
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
				req := make([]byte, 5) // [len=1][type]
				if _, err := io.ReadFull(c, req); err != nil {
					return
				}
				c.Write(reply)
			}(c)
		}
	}()
	return sock
}

// sshIdentitiesReply builds a well-formed SSH_AGENT_IDENTITIES_ANSWER for the
// given key count. A keyed reply appends filler "blobs" so n>5, modeling a real
// agent (the probe reads all n bytes via ReadFull but interprets only the count).
func sshIdentitiesReply(count uint32) []byte {
	body := make([]byte, 5)
	body[0] = 12 // SSH_AGENT_IDENTITIES_ANSWER
	binary.BigEndian.PutUint32(body[1:5], count)
	body = append(body, make([]byte, count*8)...) // dummy per-key blobs → n>5
	hdr := make([]byte, 4)
	binary.BigEndian.PutUint32(hdr, uint32(len(body)))
	return append(hdr, body...)
}

func startFakeSSHAgent(t *testing.T, count uint32) string {
	return startFakeSSHAgentRaw(t, sshIdentitiesReply(count))
}

// startFakeGPG serves a minimal Assuan dialogue: it sends greeting, consumes one
// command line, then sends reply.
func startFakeGPG(t *testing.T, greeting, reply string) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "gpg.sock")
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
				c.Write([]byte(greeting))
				r := bufio.NewReader(c)
				if _, err := r.ReadString('\n'); err != nil { // consume the command
					return
				}
				c.Write([]byte(reply))
			}(c)
		}
	}()
	return sock
}

const gpgGreeting = "OK Pleased to meet you\n"
const gpgSerial = "S SERIALNO D2760001240100000006170318250000\nOK\n"

func startFakeGPGAgent(t *testing.T, hasCard bool) string {
	if hasCard {
		return startFakeGPG(t, gpgGreeting, gpgSerial)
	}
	return startFakeGPG(t, gpgGreeting, "ERR 100696144 Operation not supported by device <SCD>\n")
}

func TestSshHasIdentities(t *testing.T) {
	cases := []struct {
		name string
		sock string
		want bool
	}{
		{"two keys", startFakeSSHAgent(t, 2), true},
		{"one key", startFakeSSHAgent(t, 1), true},
		{"empty forward", startFakeSSHAgent(t, 0), false},
		{"unreachable", filepath.Join(t.TempDir(), "nope.sock"), false},
		// fail-closed guards: a short header (n<5) and a wrong answer type must
		// route local, never hijack on a malformed / non-ssh-agent endpoint.
		{"short reply n<5", startFakeSSHAgentRaw(t, []byte{0, 0, 0, 4, 0, 0, 0, 0}), false},
		{"wrong answer type", startFakeSSHAgentRaw(t, []byte{0, 0, 0, 5, 99, 0, 0, 0, 1}), false},
	}
	for _, c := range cases {
		if got := sshHasIdentities(c.sock); got != c.want {
			t.Errorf("%s: sshHasIdentities = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestGpgCardPresent(t *testing.T) {
	cases := []struct {
		name string
		sock string
		want bool
	}{
		{"card present", startFakeGPGAgent(t, true), true},
		{"no card (key pulled)", startFakeGPGAgent(t, false), false},
		// fail-closed guards: a non-OK greeting and a bare OK with no serial must
		// both route local.
		{"bad greeting", startFakeGPG(t, "ERR 1 nope\n", ""), false},
		{"bare OK no serial", startFakeGPG(t, gpgGreeting, "OK\n"), false},
		// a status/comment line before the serial must be skipped, not break detection.
		{"serial after status line", startFakeGPG(t, gpgGreeting, "S PROGRESS card_busy\n"+gpgSerial), true},
		{"unreachable", filepath.Join(t.TempDir(), "nope.sock"), false},
	}
	for _, c := range cases {
		if got := gpgCardPresent(c.sock); got != c.want {
			t.Errorf("%s: gpgCardPresent = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestRemoteUsableDispatch(t *testing.T) {
	cases := []struct {
		name string
		set  socketSet
		want bool
	}{
		{"ssh empty", socketSet{name: "ssh", remote: startFakeSSHAgent(t, 0)}, false},
		{"ssh keyed", socketSet{name: "ssh", remote: startFakeSSHAgent(t, 1)}, true},
		{"gpg no card", socketSet{name: "gpg", remote: startFakeGPGAgent(t, false)}, false},
		{"gpg card", socketSet{name: "gpg", remote: startFakeGPGAgent(t, true)}, true},
		// unknown socket type → fail toward local (no probe, never trust connectability).
		{"unknown type", socketSet{name: "other", remote: startFakeSSHAgent(t, 5)}, false},
	}
	for _, c := range cases {
		if got := remoteUsable(c.set); got != c.want {
			t.Errorf("%s: remoteUsable = %v, want %v", c.name, got, c.want)
		}
	}
}

// A silent agent (accepts but never replies) must fail closed within the probe
// deadline, not hang — the fail-toward-local guarantee under a stuck remote.
func TestProbeFailsClosedOnTimeout(t *testing.T) {
	old := probeTimeout
	probeTimeout = 100 * time.Millisecond
	defer func() { probeTimeout = old }()

	sock := filepath.Join(t.TempDir(), "silent.sock")
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
			_ = c // hold the connection open, never reply
		}
	}()

	start := time.Now()
	if sshHasIdentities(sock) {
		t.Error("silent ssh agent: want false, got true")
	}
	if gpgCardPresent(sock) {
		t.Error("silent gpg agent: want false, got true")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("probes hung past deadline: %v", elapsed)
	}
}
