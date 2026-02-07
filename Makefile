PREFIX ?= $(HOME)/bin
VERSION ?= 0.1.0

build:
	go build -ldflags "-X grove/cmd.Version=$(VERSION)" -o grove .

install: build
	cp grove $(PREFIX)/grove

clean:
	rm -f grove
