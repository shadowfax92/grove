PREFIX ?= $(HOME)/bin
VERSION ?= 0.1.0
FISH_FUNCTIONS ?= $(HOME)/.config/fish/functions

build:
	go build -ldflags "-X grove/cmd.Version=$(VERSION)" -o grove .

install: build fish
	@set -eu; temporary=$$(mktemp "$(PREFIX)/.grove-install.XXXXXX"); \
	trap 'rm -f "$$temporary"' EXIT; \
	cp grove "$$temporary"; \
	chmod 755 "$$temporary"; \
	codesign --force --sign - "$$temporary"; \
	mv -f "$$temporary" "$(PREFIX)/grove"

fish:
	mkdir -p $(FISH_FUNCTIONS)
	cp gv.fish $(FISH_FUNCTIONS)/gv.fish

uninstall:
	rm -f $(PREFIX)/grove
	rm -f $(FISH_FUNCTIONS)/gv.fish

clean:
	rm -f grove
