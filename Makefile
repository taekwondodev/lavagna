install:
	CGO_ENABLED=0 go build -trimpath -o $(HOME)/.local/bin/lavagna .
ifeq ($(shell uname),Darwin)
	sh macos/notifier/install.sh $(HOME)/Applications/Lavagna.app
endif

.PHONY: install
