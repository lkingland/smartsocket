.PHONY: build install uninstall enable disable status

# User units go where systemd looks for packaged user units, so ~/.config stays
# the desktop's to manage. `systemctl --user mask` (in enable) still writes its
# /dev/null links into ~/.config/systemd/user; that is systemd's choice.
UNITDIR ?= $(HOME)/.local/share/systemd/user

build:
	go build -o smartsocket ./cmd/smartsocket
	go build -o pinentry-smart ./cmd/pinentry-smart

install: build
	install -Dm755 smartsocket ~/.local/bin/smartsocket
	install -Dm755 pinentry-smart ~/.local/bin/pinentry-smart
	install -Dm644 smartsocket.service $(UNITDIR)/smartsocket.service
	install -Dm644 smartsocket-gpg.socket $(UNITDIR)/smartsocket-gpg.socket
	install -Dm644 smartsocket-ssh.socket $(UNITDIR)/smartsocket-ssh.socket
	install -Dm644 gpg-agent-local.socket $(UNITDIR)/gpg-agent-local.socket
	install -Dm644 gpg-agent-ssh-local.socket $(UNITDIR)/gpg-agent-ssh-local.socket
	install -Dm644 gpg-agent-local.service $(UNITDIR)/gpg-agent-local.service
	systemctl --user daemon-reload
	systemctl --user try-restart smartsocket.service

uninstall: disable
	rm -f ~/.local/bin/smartsocket
	rm -f ~/.local/bin/pinentry-smart
	rm -f $(UNITDIR)/smartsocket.service
	rm -f $(UNITDIR)/smartsocket-gpg.socket
	rm -f $(UNITDIR)/smartsocket-ssh.socket
	rm -f $(UNITDIR)/gpg-agent-local.socket
	rm -f $(UNITDIR)/gpg-agent-ssh-local.socket
	rm -f $(UNITDIR)/gpg-agent-local.service
	systemctl --user daemon-reload

enable:
	systemctl --user mask gpg-agent.socket gpg-agent-ssh.socket gpg-agent.service
	systemctl --user enable --now gpg-agent-local.socket gpg-agent-ssh-local.socket
	systemctl --user enable --now smartsocket-gpg.socket smartsocket-ssh.socket

disable:
	systemctl --user disable --now smartsocket-gpg.socket smartsocket-ssh.socket || true
	systemctl --user disable --now gpg-agent-local.socket gpg-agent-ssh-local.socket || true
	systemctl --user unmask gpg-agent.socket gpg-agent-ssh.socket gpg-agent.service

status:
	@echo "=== Smartsockets ==="
	@systemctl --user status smartsocket-gpg.socket smartsocket-ssh.socket --no-pager || true
	@echo ""
	@echo "=== Smartsocket service ==="
	@systemctl --user status smartsocket.service --no-pager || true
	@echo ""
	@echo "=== Local GPG agent sockets ==="
	@systemctl --user status gpg-agent-local.socket gpg-agent-ssh-local.socket --no-pager || true
