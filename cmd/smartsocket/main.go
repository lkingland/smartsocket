package main

import (
	"bufio"
	"encoding/binary"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const socketDir = "/run/user/1000/gnupg"

// SD_LISTEN_FDS_START is the first file descriptor passed by systemd
const SD_LISTEN_FDS_START = 3

// socketSet defines a triplet of sockets for proxying
type socketSet struct {
	name   string // for logging
	fdName string // FileDescriptorName from systemd socket unit
	smart  string // where we listen (standalone mode only)
	remote string // forwarded from laptop
	local  string // local gpg-agent
}

var sockets = []socketSet{
	{
		name:   "ssh",
		fdName: "ssh",
		smart:  socketDir + "/S.gpg-agent.ssh.smart",
		remote: socketDir + "/S.gpg-agent.ssh.remote",
		local:  socketDir + "/S.gpg-agent.ssh.local",
	},
	{
		name:   "gpg",
		fdName: "gpg",
		smart:  socketDir + "/S.gpg-agent.smart",
		remote: socketDir + "/S.gpg-agent.remote",
		local:  socketDir + "/S.gpg-agent.local",
	},
}

func main() {
	runProxy()
}

func runProxy() {
	var wg sync.WaitGroup
	shutdown := make(chan struct{})

	// Check for systemd socket activation
	listeners, err := getSystemdListeners()
	if err != nil {
		log.Fatalf("failed to get systemd listeners: %v", err)
	}

	if listeners != nil {
		// Socket activated mode
		log.Println("starting in socket-activated mode")
		for _, s := range sockets {
			if ln, ok := listeners[s.fdName]; ok {
				wg.Add(1)
				go runProxyWithListener(s, ln, &wg, shutdown)
			}
		}
	} else {
		// Standalone mode (for testing or non-systemd systems)
		log.Println("starting in standalone mode")
		for _, s := range sockets {
			wg.Add(1)
			go runProxyStandalone(s, &wg, shutdown)
		}
	}

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("shutting down")
	close(shutdown)

	// Clean up sockets (standalone mode only)
	if listeners == nil {
		for _, s := range sockets {
			os.Remove(s.smart)
		}
	}

	wg.Wait()
}

// getSystemdListeners returns listeners passed via socket activation.
// Returns nil if not socket activated.
func getSystemdListeners() (map[string]net.Listener, error) {
	pid, err := strconv.Atoi(os.Getenv("LISTEN_PID"))
	if err != nil || pid != os.Getpid() {
		return nil, nil // not socket activated
	}

	nfds, err := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	if err != nil || nfds == 0 {
		return nil, nil
	}

	// Parse LISTEN_FDNAMES (colon-separated list)
	names := strings.Split(os.Getenv("LISTEN_FDNAMES"), ":")

	listeners := make(map[string]net.Listener)
	for i := 0; i < nfds; i++ {
		fd := SD_LISTEN_FDS_START + i
		f := os.NewFile(uintptr(fd), "systemd-socket")
		ln, err := net.FileListener(f)
		f.Close() // FileListener dups the fd
		if err != nil {
			return nil, err
		}

		name := ""
		if i < len(names) {
			name = names[i]
		}
		listeners[name] = ln
	}

	// Clear env to prevent leaking to children
	os.Unsetenv("LISTEN_PID")
	os.Unsetenv("LISTEN_FDS")
	os.Unsetenv("LISTEN_FDNAMES")

	return listeners, nil
}

// runProxyWithListener runs the proxy with a listener from systemd socket activation
func runProxyWithListener(s socketSet, ln net.Listener, wg *sync.WaitGroup, shutdown chan struct{}) {
	defer wg.Done()
	defer ln.Close()

	log.Printf("[%s] listening (socket activated)", s.name)
	log.Printf("[%s]   remote: %s", s.name, s.remote)
	log.Printf("[%s]   local:  %s", s.name, s.local)

	// Close listener on shutdown signal
	go func() {
		<-shutdown
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Listener closed
			if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
				return
			}
			log.Printf("[%s] accept error: %v", s.name, err)
			continue
		}
		go proxy(conn, s)
	}
}

// runProxyStandalone listens on the smart socket and proxies to remote or local (standalone mode)
func runProxyStandalone(s socketSet, wg *sync.WaitGroup, shutdown chan struct{}) {
	defer wg.Done()

	// Clean up any existing socket
	os.Remove(s.smart)

	ln, err := net.Listen("unix", s.smart)
	if err != nil {
		log.Printf("[%s] failed to listen on %s: %v", s.name, s.smart, err)
		return
	}
	defer ln.Close()

	log.Printf("[%s] listening on %s", s.name, s.smart)
	log.Printf("[%s]   remote: %s", s.name, s.remote)
	log.Printf("[%s]   local:  %s", s.name, s.local)

	// Close listener on shutdown signal
	go func() {
		<-shutdown
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Listener closed
			if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
				return
			}
			log.Printf("[%s] accept error: %v", s.name, err)
			continue
		}
		go proxy(conn, s)
	}
}

func proxy(client net.Conn, s socketSet) {
	defer client.Close()

	target := s.local
	if remoteUsable(s) {
		target = s.remote
	}

	backend, err := net.Dial("unix", target)
	if err != nil {
		log.Printf("[%s] failed to connect to %s: %v", s.name, target, err)
		return
	}
	defer backend.Close()

	done := make(chan struct{})

	go func() {
		io.Copy(backend, client)
		done <- struct{}{}
	}()

	go func() {
		io.Copy(client, backend)
		done <- struct{}{}
	}()

	// Wait for either direction to complete
	<-done
}

// isAvailable checks if a socket is connectable
func isAvailable(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, dialTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// Probe timeouts. Vars (not consts) so tests can shrink them.
var (
	dialTimeout  = 500 * time.Millisecond
	probeTimeout = 1 * time.Second
)

// remoteUsable reports whether the remote socket is not merely connectable but
// actually backed by a usable key. This is the fix for the empty-forward hijack:
// when the operator pulls the YubiKey from the laptop but the SSH session lingers,
// the forwarded agent is connectable-but-empty — we must fall back to the local
// agent (the YubiKey plugged into this host). It is protocol-specific because the
// "has a usable key" question differs between the ssh-agent and gpg/Assuan sockets.
// Any ambiguity (unreachable, empty, malformed reply, timeout) → false → route local.
func remoteUsable(s socketSet) bool {
	switch s.name {
	case "ssh":
		return sshHasIdentities(s.remote)
	case "gpg":
		return gpgCardPresent(s.remote)
	default:
		// Unknown socket type: we have no key-aware probe for it, so honor the
		// fail-toward-local invariant uniformly rather than reverting to the old
		// connectability-only behavior. A new socket type must add its own probe.
		return false
	}
}

// sshHasIdentities speaks the ssh-agent protocol: it sends REQUEST_IDENTITIES and
// reports whether the agent offered at least one key. An empty forwarded agent
// (key pulled on the client) answers with zero identities → false → route local.
func sshHasIdentities(socket string) bool {
	const (
		requestIdentities = 11 // SSH_AGENTC_REQUEST_IDENTITIES
		identitiesAnswer  = 12 // SSH_AGENT_IDENTITIES_ANSWER
		maxReply          = 256 * 1024
	)
	conn, err := net.DialTimeout("unix", socket, dialTimeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(probeTimeout))

	// Request: [uint32 length=1][byte type].
	if _, err := conn.Write([]byte{0, 0, 0, 1, requestIdentities}); err != nil {
		return false
	}
	var hdr [4]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil {
		return false
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n < 5 || n > maxReply { // need a type byte + a 4-byte count
		return false
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(conn, body); err != nil {
		return false
	}
	if body[0] != identitiesAnswer {
		return false
	}
	return binary.BigEndian.Uint32(body[1:5]) >= 1
}

// gpgCardPresent speaks the Assuan protocol: it sends SCD SERIALNO and reports
// whether a smartcard (the YubiKey) is reachable through the agent — i.e. whether
// the agent can actually sign. The operator's signing key IS the card, so a card
// serial is the truest "this agent can sign" signal. No card (key pulled) →
// the agent replies ERR → false → route local.
func gpgCardPresent(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, dialTimeout)
	if err != nil {
		return false
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(probeTimeout))

	r := bufio.NewReader(conn)
	greeting, err := r.ReadString('\n')
	if err != nil || !strings.HasPrefix(greeting, "OK") {
		return false
	}
	if _, err := conn.Write([]byte("SCD SERIALNO\n")); err != nil {
		return false
	}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return false
		}
		switch {
		case strings.HasPrefix(line, "S SERIALNO"):
			return true
		case strings.HasPrefix(line, "ERR"):
			return false
		case strings.HasPrefix(line, "OK"):
			return false // reached OK without a serial — no usable card, fail toward local
		}
		// ignore other status / comment lines; keep reading
	}
}
