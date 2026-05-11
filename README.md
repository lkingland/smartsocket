# smartsocket

Installs a socket router which chooses local or remote sockets for GPG
signing and authentication based on availability.

Problem:

I want to leave my desk, taking my YubiKey with me, and have both auth
(ssh connections) and signing (git commits) work when connecting remotely over
an ssh connection.

I should also be able to use the desktop/server directly, key plugged in as
normal, whether or not there exists an active ssh connection from a client with
a key inserted.

Solution:

A socket proxy replacing the standard GPG sockets which intelligently
routes ssh auth and gpg signing requests to either the local agent, or to auth
forwarded socket from a remote connection.

## Use Cases

SSH Authentication and signing should work when either local to a stationary
desktop/server where smartsocket is installed, or when connecting from
a remote client such as a laptop or other physically distant server.

### When Local: 
When server-local (not connected remotely via ssh) I am able to make SSH
connections and sign git commits using a YubiKey plugged in locally.

### When Remote - Single Key:
When in physical posession of the key, I should be able to ssh and sign:
- Locally on the connecting client as usual
- Remotely over an ssh connection (via agent forwarding) using the key in my
  posession.

### When Remote - Dual Keys:
When both in possession of a key, and a duplicate key is left inserted in the
server, I should be able to ssh auth and sign commits:
- Locally on the connecting client using the key connected to the client.
- Remotely on the server over the ssh connection using the key connected to
  the client, taking precedence over the key connected to the server.
- If the server's key is removed, it should seamlessly transition to the
  single key use case above.

**Note:** Routing is based on socket availability, not key presence. If you SSH
in without your key (remote socket is connectable but empty), smartsocket will
still route to remote. To fall back to the server's key, disconnect the SSH
session so the remote socket is no longer available.


## How It Works

Smartsocket uses systemd socket activation to transparently intercept
connections to the standard gpg-agent socket paths:

```
S.gpg-agent     → [systemd] → smartsocket → S.gpg-agent.remote  (forwarded)
                                          ↘ S.gpg-agent.local   → [systemd] → gpg-agent

S.gpg-agent.ssh → [systemd] → smartsocket → S.gpg-agent.ssh.remote  (forwarded)
                                          ↘ S.gpg-agent.ssh.local   → [systemd] → gpg-agent
```

For each connection, smartsocket probes the remote socket (500ms timeout).
If remote is available, it proxies there; otherwise, it proxies to local.

**No configuration needed** - clients use standard socket paths and smartsocket
handles the routing transparently.

## Installation

```bash
make install
make enable
```

This will:
1. Install the smartsocket binary and systemd units
2. Mask the original gpg-agent socket units
3. Enable the smartsocket and local gpg-agent socket units

## Socket Paths

**Standard paths (intercepted by smartsocket):**
- `/run/user/1000/gnupg/S.gpg-agent` - GPG operations
- `/run/user/1000/gnupg/S.gpg-agent.ssh` - SSH authentication

**Remote sockets (forwarded from laptop via SSH):**
- `/run/user/1000/gnupg/S.gpg-agent.remote`
- `/run/user/1000/gnupg/S.gpg-agent.ssh.remote`

**Local sockets (local gpg-agent fallback):**
- `/run/user/1000/gnupg/S.gpg-agent.local`
- `/run/user/1000/gnupg/S.gpg-agent.ssh.local`

## SSH Client Configuration (Laptop)

On the machine you SSH *from* (e.g., your laptop with the YubiKey), configure
SSH to forward both gpg-agent sockets.

### Prerequisites

Ensure gpg-agent is running with SSH support on your laptop:

```bash
# ~/.gnupg/gpg-agent.conf
enable-ssh-support
```

### SSH Config

Add to `~/.ssh/config` on your laptop:

```
Host myserver
    # Forward GPG agent socket (for signing)
    RemoteForward /run/user/1000/gnupg/S.gpg-agent.remote /path/to/local/S.gpg-agent

    # Forward SSH agent socket
    RemoteForward /run/user/1000/gnupg/S.gpg-agent.ssh.remote /path/to/local/S.gpg-agent.ssh

    # Allow SSH to overwrite stale sockets on reconnect
    StreamLocalBindUnlink yes
```

### Finding Your Local Socket Paths

```bash
# GPG socket
gpgconf --list-dirs agent-socket
# Linux: /run/user/1000/gnupg/S.gpg-agent
# macOS: /Users/username/.gnupg/S.gpg-agent

# SSH socket
gpgconf --list-dirs agent-ssh-socket
# Linux: /run/user/1000/gnupg/S.gpg-agent.ssh
# macOS: /Users/username/.gnupg/S.gpg-agent.ssh
```

### Server-Side sshd Configuration

On the target machine, ensure `/etc/ssh/sshd_config` includes:

```
StreamLocalBindUnlink yes
```

This allows SSH to clean up stale forwarded sockets on reconnect.

## Shell Configuration

Set `SSH_AUTH_SOCK` to the standard path in your shell config:

```bash
# .bashrc / .zshrc
export SSH_AUTH_SOCK=/run/user/1000/gnupg/S.gpg-agent.ssh
```

```nu
# config.nu
$env.SSH_AUTH_SOCK = "/run/user/1000/gnupg/S.gpg-agent.ssh"
```

## Known Issues

### Free-running gpg-agent steals smartsocket's sockets

Symptom: connections via `S.gpg-agent` or `S.gpg-agent.ssh` bypass
smartsocket and hit a local gpg-agent instead — e.g. `ssh-add -L`
returns "no identities" or only the local key when a remote key
should be in play. `ss -lxn | grep S.gpg-agent` shows two listeners
on the same path (smartsocket's, backlog 4096; the rogue's, backlog
64).

Cause: any process that runs `gpg-agent --use-standard-socket --daemon`
(directly or via `gpgconf --launch gpg-agent`, or via `gpg-connect-agent`
without `--no-autostart`) will `unlink()` smartsocket's socket file at
`/run/user/<uid>/gnupg/S.gpg-agent{,.ssh}` and re-bind its own listener
there. systemd's listener inode survives in the kernel but the
filesystem path now points to the rogue, so smartsocket is bypassed.

Recovery: restart the smartsocket socket units so they re-bind the
filesystem paths.

```bash
pkill -f 'gpg-agent.*--daemon'
systemctl --user restart smartsocket-gpg.socket smartsocket-ssh.socket
```

Prevention: keep shell rc files and SSH config from auto-launching
gpg-agent.

- Remove `gpgconf --launch gpg-agent` from shell startup (`.bashrc`,
  `config.nu`, etc.) — smartsocket + `gpg-agent-local.service` are
  socket-activated, so no explicit launch is needed.
- If you have a `Match host * exec "gpg-connect-agent UPDATESTARTUPTTY
  /bye"` block in `~/.ssh/config` for pinentry-tty integration, add
  `--no-autostart`:
  ```
  Match host * exec "gpg-connect-agent --no-autostart UPDATESTARTUPTTY /bye"
  ```

## Management

```bash
make status    # Check all socket and service status
make disable   # Stop smartsocket and restore original gpg-agent
make enable    # Enable smartsocket (masks original gpg-agent)
make uninstall # Remove everything
```

## Standalone Mode

For testing or non-systemd systems, smartsocket can run in standalone mode
where it creates its own `.smart` suffixed sockets:

```bash
./smartsocket
```

In standalone mode, you'll need to configure clients to use the `.smart` paths.

## Systemd Units

**Smartsocket:**
- `smartsocket-gpg.socket` - Listens on `S.gpg-agent`
- `smartsocket-ssh.socket` - Listens on `S.gpg-agent.ssh`
- `smartsocket.service` - The proxy service

**Local gpg-agent:**
- `gpg-agent-local.socket` - Listens on `S.gpg-agent.local`
- `gpg-agent-ssh-local.socket` - Listens on `S.gpg-agent.ssh.local`
- `gpg-agent-local.service` - Local gpg-agent instance
