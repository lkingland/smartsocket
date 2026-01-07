package main

import (
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

const (
	smartSocket  = "/run/user/1000/gnupg/S.gpg-agent.ssh.smart"
	remoteSocket = "/run/user/1000/gnupg/S.gpg-agent.ssh.remote"
	localSocket  = "/run/user/1000/gnupg/S.gpg-agent.ssh"
)

// hasIdentities checks if the socket has at least one SSH key available
func hasIdentities(socket string) bool {
	cmd := exec.Command("ssh-add", "-l")
	cmd.Env = append(os.Environ(), "SSH_AUTH_SOCK="+socket)
	err := cmd.Run()
	return err == nil // exit 0 = has keys
}

// checkDependencies verifies required tools are available
func checkDependencies() {
	if _, err := exec.LookPath("ssh-add"); err != nil {
		log.Fatal("ssh-add not found. Please install openssh.")
	}
}

func proxy(client net.Conn) {
	defer client.Close()

	target := localSocket
	if hasIdentities(remoteSocket) {
		target = remoteSocket
	}

	backend, err := net.Dial("unix", target)
	if err != nil {
		log.Printf("failed to connect to %s: %v", target, err)
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

func main() {
	checkDependencies()

	// Clean up any existing socket
	os.Remove(smartSocket)

	ln, err := net.Listen("unix", smartSocket)
	if err != nil {
		log.Fatalf("failed to listen on %s: %v", smartSocket, err)
	}
	defer ln.Close()

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		log.Println("shutting down")
		ln.Close()
		os.Remove(smartSocket)
		os.Exit(0)
	}()

	log.Printf("smartsocket listening on %s", smartSocket)
	log.Printf("  remote: %s", remoteSocket)
	log.Printf("  local:  %s", localSocket)

	for {
		conn, err := ln.Accept()
		if err != nil {
			// Listener closed
			if opErr, ok := err.(*net.OpError); ok && opErr.Err.Error() == "use of closed network connection" {
				return
			}
			log.Printf("accept error: %v", err)
			continue
		}
		go proxy(conn)
	}
}
