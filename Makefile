VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT ?= $(shell git rev-parse --short HEAD)
DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
BUILT_BY ?= make

fmt:
	go fmt ./...
run:fmt
	go run main.go server -c config.yml -e -v
build:
	goreleaser release --snapshot
docker_build:
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg DATE=$(DATE) \
		--build-arg BUILT_BY=$(BUILT_BY) \
		-t bendusy/idc-alert-feishu .
docker_push:docker_build
	docker push bendusy/idc-alert-feishu
