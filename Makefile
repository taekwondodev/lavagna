install:
	CGO_ENABLED=0 go build -trimpath -o $(HOME)/.local/bin/lavagna .

.PHONY: install
