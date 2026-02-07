PREFIX ?= $(HOME)/bin

build:
	go build -o grove .

install: build
	cp grove $(PREFIX)/grove

clean:
	rm -f grove
