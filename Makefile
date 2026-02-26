PREFIX ?= $(HOME)/bin
VERSION ?= 0.1.0
FISH_FUNCTIONS ?= $(HOME)/.config/fish/functions

build:
	go build -ldflags "-X grove/cmd.Version=$(VERSION)" -o grove .

install: build
	cp grove $(PREFIX)/grove
	codesign --force --sign - $(PREFIX)/grove

fish:
	mkdir -p $(FISH_FUNCTIONS)
	cp gv.fish $(FISH_FUNCTIONS)/gv.fish

clean:
	rm -f grove
