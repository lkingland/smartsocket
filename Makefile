.PHONY: build install uninstall enable disable status

build:
	go build -o smartsocket .

install: build
	install -Dm755 smartsocket ~/.local/bin/smartsocket
	install -Dm644 smartsocket.service ~/.config/systemd/user/smartsocket.service
	install -Dm644 smartsocket-gpg.socket ~/.config/systemd/user/smartsocket-gpg.socket
	install -Dm644 smartsocket-ssh.socket ~/.config/systemd/user/smartsocket-ssh.socket
	~/.local/bin/smartsocket generate
	systemctl --user daemon-reload

uninstall: disable
	rm -f ~/.local/bin/smartsocket
	rm -f ~/.config/systemd/user/smartsocket.service
	rm -f ~/.config/systemd/user/smartsocket-gpg.socket
	rm -f ~/.config/systemd/user/smartsocket-ssh.socket
	rm -f ~/.config/systemd/user/gpg-agent-local.socket
	rm -f ~/.config/systemd/user/gpg-agent-ssh-local.socket
	rm -f ~/.config/systemd/user/gpg-agent-local.service
	systemctl --user daemon-reload

enable:
	systemctl --user mask gpg-agent.socket gpg-agent-ssh.socket gpg-agent.service
	systemctl --user start gpg-agent-local.socket gpg-agent-ssh-local.socket
	systemctl --user enable --now smartsocket-gpg.socket smartsocket-ssh.socket

disable:
	systemctl --user disable --now smartsocket-gpg.socket smartsocket-ssh.socket || true
	systemctl --user stop gpg-agent-local.socket gpg-agent-ssh-local.socket || true
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
