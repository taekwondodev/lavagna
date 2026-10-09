install:
	CGO_ENABLED=0 go build -trimpath -o $(HOME)/.local/bin/lavagna .
ifeq ($(shell uname),Darwin)
	sh macos/notifier/install.sh $(HOME)/Applications/Lavagna.app
endif
	-$(HOME)/.local/bin/lavagna hermes-hooks install

uninstall:
	-$(HOME)/.local/bin/lavagna hermes-hooks remove
	rm -f $(HOME)/.local/bin/lavagna
ifeq ($(shell uname),Darwin)
	rm -rf $(HOME)/Applications/Lavagna.app
endif

.PHONY: install uninstall
