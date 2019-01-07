ifeq ($(VERSION),)
ifeq (, $(wildcard ./.git))
	VERSION := _unknown
else
	VERSION := $(shell git describe --always --dirty --tags --match "release/*" | sed 's|release/||g')
endif
endif

REGISTRY_IMAGE ?= "frozy/connector"

all:	deps build dist

deps:
	GOOS=linux   GOARCH=amd64 go get -d -v
	GOOS=windows GOARCH=amd64 go get -d -v

build:
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -tags netgo -ldflags '-w -s -X gitlab.com/frozy.io/connector/app.Version=${VERSION}' -o bin/connector-linux-amd64-v${VERSION}
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags netgo -ldflags '-w -s -X gitlab.com/frozy.io/connector/app.Version=${VERSION}' -o bin/connector-windows-amd64-v${VERSION}.exe

dist: build
	mkdir -p dist/
	tar czvf dist/connector-linux-amd64-v${VERSION}.tar.gz -C bin connector-linux-amd64-v${VERSION}
	zip -j dist/connector-windows-amd64-v${VERSION}.zip bin/connector-windows-amd64-v${VERSION}.exe

distclean:
	rm -rf dist/

clean:
	rm -rf bin/ dist/

image:
	docker build --pull --build-arg VERSION=${VERSION} -t ${REGISTRY_IMAGE}:${VERSION} .
