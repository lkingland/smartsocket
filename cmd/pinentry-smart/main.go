// Command pinentry-smart routes the GPG PIN prompt to the remote client's native
// pinentry (e.g. pinentry-mac) over a reverse-forwarded socket when the client is
// connected, and falls back to a local pinentry otherwise.
//
// gpg-agent execs this and speaks the Assuan pinentry protocol over stdin/stdout.
// In Assuan the pinentry (server) speaks first with an "OK" greeting, so we dial
// the forwarded socket and only commit once we've seen that greeting; any failure
// before then leaves gpg-agent's stdin untouched, so the fallback gets a clean
// dialogue. The forwarded socket exists iff the client->server SSH session is up
// (the client RemoteForwards it; sshd StreamLocalBindUnlink=yes clears it on
// teardown), so its presence is a reliable "client connected" signal.
// See README "Pinentry Forwarding".
package main

import (
	"bufio"
	"io"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// socketPath is the forwarded pinentry socket; overridable for testing.
func socketPath() string {
	if s := os.Getenv("PINENTRY_SMART_SOCKET"); s != "" {
		return s
	}
	return "/run/user/" + strconv.Itoa(os.Getuid()) + "/gnupg/S.pinentry"
}

// fallbackProgram is the local pinentry used when no remote is available.
func fallbackProgram() string {
	if p := os.Getenv("PINENTRY_SMART_FALLBACK"); p != "" {
		return p
	}
	return "/usr/bin/pinentry-curses"
}

func main() {
	if forward(os.Stdin, os.Stdout, socketPath()) {
		return
	}
	fallback()
}

// forward relays the Assuan pinentry dialogue to a live remote pinentry over the
// forwarded socket. It returns false WITHOUT reading from in if the far end is not
// a live pinentry (no socket, dial failure, or no "OK" greeting), so the caller can
// fall back cleanly — gpg-agent waits for the greeting before sending anything, so
// in is untouched until we've committed.
func forward(in io.Reader, out io.Writer, sock string) bool {
	fi, err := os.Stat(sock)
	if err != nil || fi.Mode()&os.ModeSocket == 0 {
		return false
	}
	conn, err := net.DialTimeout("unix", sock, 2*time.Second)
	if err != nil {
		return false
	}
	// A live pinentry must send the Assuan "OK" greeting. A down/stale responder
	// (socket present but no listener behind the tunnel) yields EOF or garbage.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	br := bufio.NewReader(conn)
	greeting, err := br.ReadString('\n')
	if err != nil || !strings.HasPrefix(greeting, "OK") {
		conn.Close()
		return false
	}
	_ = conn.SetReadDeadline(time.Time{}) // interactive from here on

	io.WriteString(out, greeting) // relay the greeting we already consumed
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		io.Copy(conn, in)
		if uc, ok := conn.(*net.UnixConn); ok {
			uc.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		io.Copy(out, br) // br first, to drain anything buffered after the greeting
	}()
	wg.Wait()
	conn.Close()
	return true
}

func fallback() {
	cmd := exec.Command(fallbackProgram(), os.Args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}
