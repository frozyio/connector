REGISTRY_IMAGE ?= "frozy/connector"
ifneq ("$(REGISTRY_IMAGE_TAG)", "")
	TAG_SECTION=:$(REGISTRY_IMAGE_TAG)
endif

all: build

build:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags netgo -ldflags '-w -s' -o connector

deps:
	go get -d -v

image:
	docker build --pull -t ${REGISTRY_IMAGE}${TAG_SECTION} .
