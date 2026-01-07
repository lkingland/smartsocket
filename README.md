# smartsocket

Use a YubiKey for GPG auth when either local or connected remotely via ssh.

A smart GPG SSH agent socket proxy that dynamically routes to either a remote
forwarded socket or a local gpg-agent socket.

## Use Case

When SSH'd into a machine from a laptop with a YubiKey, use the laptop's
forwarded gpg-agent. When working locally, use the local gpg-agent. This proxy
automatically detects which is available and routes accordingly.

## How It Works

1. Listens on `/run/user/1000/gnupg/S.gpg-agent.ssh.smart`
2. For each connection, probes the remote socket (500ms timeout)
3. If remote is alive, proxies to remote; otherwise, proxies to local
4. `SSH_AUTH_SOCK` always points to the smart socket

## Installation

```bash
make install
make enable
```

## Configuration

Set in your shell config (`.bashrc`, `config.nu`, etc.):

```bash
export SSH_AUTH_SOCK=/run/user/1000/gnupg/S.gpg-agent.ssh.smart
```

## Socket Paths

- **Smart socket**: `/run/user/1000/gnupg/S.gpg-agent.ssh.smart`
- **Remote socket**: `/run/user/1000/gnupg/S.gpg-agent.ssh.remote` (forwarded via SSH)
- **Local socket**: `/run/user/1000/gnupg/S.gpg-agent.ssh` (local gpg-agent)

## SSH Client Configuration (Laptop/Remote Machine)

On the machine you SSH *from* (e.g., your laptop with the YubiKey), configure
SSH to forward the gpg-agent socket.

### Prerequisites

Ensure gpg-agent is running with SSH support on your laptop:

```bash
# ~/.gnupg/gpg-agent.conf
enable-ssh-support
```

### SSH Config

Add to `~/.ssh/config` on your laptop:

```
Host aurora  # or your target hostname
    # Forward the SSH agent socket
    RemoteForward /run/user/1000/gnupg/S.gpg-agent.ssh.remote /path/to/local/S.gpg-agent.ssh

    # Allow SSH to overwrite stale sockets on reconnect
    StreamLocalBindUnlink yes
```

### Finding Your Local Socket Path

The local socket path varies by OS:

```bash
# Linux
gpgconf --list-dirs agent-ssh-socket
# Usually: /run/user/1000/gnupg/S.gpg-agent.ssh

# macOS
gpgconf --list-dirs agent-ssh-socket
# Usually: /Users/username/.gnupg/S.gpg-agent.ssh
```

### Server-Side sshd Configuration

On the target machine, ensure `/etc/ssh/sshd_config` includes:

```
StreamLocalBindUnlink yes
```

This allows SSH to clean up stale forwarded sockets on reconnect.

## Management

```bash
make status   # Check service status
make disable  # Stop and disable
make enable   # Start and enable
make uninstall # Remove everything
```
