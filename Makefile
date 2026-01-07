.PHONY: build install uninstall enable disable status

build:
	go build -o smartsocket .

install: build
	install -Dm755 smartsocket ~/.local/bin/smartsocket
	install -Dm644 smartsocket.service ~/.config/systemd/user/smartsocket.service
	systemctl --user daemon-reload

uninstall:
	systemctl --user stop smartsocket.service || true
	systemctl --user disable smartsocket.service || true
	rm -f ~/.local/bin/smartsocket
	rm -f ~/.config/systemd/user/smartsocket.service
	systemctl --user daemon-reload

enable:
	systemctl --user enable --now smartsocket.service

disable:
	systemctl --user disable --now smartsocket.service

status:
	systemctl --user status smartsocket.service
