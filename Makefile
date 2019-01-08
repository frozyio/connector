ifeq ($(VERSION),)
ifeq (, $(wildcard ./.git))
	VERSION := _unknown
else
	VERSION := $(shell git describe --always --dirty --tags --match "release/*" | sed 's|release/||g')
	VERSION_MACOS := $(shell echo '${VERSION}' | sed  's|\.|_|g')
endif
endif

REGISTRY_IMAGE ?= "frozy/connector"

all:	deps build dist

deps:
	GOOS=linux   GOARCH=amd64 go get -t -d -v ./...
	GOOS=linux   GOARCH=arm   go get -t -d -v ./...
	GOOS=windows GOARCH=amd64 go get -t -d -v ./...
	GOOS=darwin  GOARCH=amd64 go get -t -d -v ./...

build:
	CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -tags netgo -ldflags '-w -s -X gitlab.com/frozy.io/connector/app.Version=${VERSION}' -o bin/connector-linux-amd64-v${VERSION}
	CGO_ENABLED=0 GOOS=linux   GOARCH=arm   go build -tags netgo -ldflags '-w -s -X gitlab.com/frozy.io/connector/app.Version=${VERSION}' -o bin/connector-linux-arm-v${VERSION}
	CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -tags netgo -ldflags '-w -s -X gitlab.com/frozy.io/connector/app.Version=${VERSION}' -o bin/connector-windows-amd64-v${VERSION}.exe
	CGO_ENABLED=0 GOOS=darwin  GOARCH=amd64 go build -tags netgo -ldflags '-w -s -X gitlab.com/frozy.io/connector/app.Version=${VERSION}' \
		    -o bin/connector-macos-darwin-amd64-v${VERSION_MACOS}

dist: build
	mkdir -p dist/
	tar czvf dist/connector-linux-amd64-v${VERSION}.tar.gz -C bin connector-linux-amd64-v${VERSION}
	tar czvf dist/connector-linux-arm-v${VERSION}.tar.gz -C bin connector-linux-arm-v${VERSION}
	zip -j dist/connector-windows-amd64-v${VERSION}.zip bin/connector-windows-amd64-v${VERSION}.exe
	zip -j dist/connector-macos-darwin-amd64-v${VERSION_MACOS}.zip bin/connector-macos-darwin-amd64-v${VERSION_MACOS}

distclean:
	rm -rf dist/

clean:
	rm -rf bin/ dist/

image:
	docker build --pull \
		--build-arg VERSION=${VERSION} \
		--build-arg GOPROXY=${GOPROXY} \
		-t ${REGISTRY_IMAGE}:${VERSION} .
