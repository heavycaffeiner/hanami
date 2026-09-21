.PHONY: test race vet test-cgo build run-api run-worker run-platform run-secured run-reconfigurable

test:
	go test ./...
	(cd api && go test ./...)
	(cd config && go test ./...)
	(cd gin && go test ./...)
	(cd observability && go test ./...)
	(cd starters/api && go test ./...)
	(cd starters/worker && go test ./...)
	(cd cli && go test ./...)
	(cd database && go test ./...)
	(cd migration && go test ./...)
	(cd scheduler && go test ./...)
	(cd starters/platform && go test ./...)

race:
	go test -race ./...
	(cd api && go test -race ./...)
	(cd config && go test -race ./...)
	(cd gin && go test -race ./...)
	(cd observability && go test -race ./...)
	(cd starters/api && go test -race ./...)
	(cd starters/worker && go test -race ./...)
	(cd cli && go test -race ./...)
	(cd database && go test -race ./...)
	(cd migration && go test -race ./...)
	(cd scheduler && go test -race ./...)
	(cd starters/platform && go test -race ./...)

vet:
	go vet ./...
	(cd api && go vet ./...)
	(cd config && go vet ./...)
	(cd gin && go vet ./...)
	(cd observability && go vet ./...)
	(cd starters/api && go vet ./...)
	(cd starters/worker && go vet ./...)
	(cd cli && go vet ./...)
	(cd database && go vet ./...)
	(cd migration && go vet ./...)
	(cd scheduler && go vet ./...)
	(cd starters/platform && go vet ./...)

test-cgo:
	CGO_ENABLED=0 go test ./...
	(cd api && CGO_ENABLED=0 go test ./...)
	(cd config && CGO_ENABLED=0 go test ./...)
	(cd gin && CGO_ENABLED=0 go test ./...)
	(cd observability && CGO_ENABLED=0 go test ./...)
	(cd starters/api && CGO_ENABLED=0 go test ./...)
	(cd starters/worker && CGO_ENABLED=0 go test ./...)
	(cd cli && CGO_ENABLED=0 go test ./...)
	(cd database && CGO_ENABLED=0 go test ./...)
	(cd migration && CGO_ENABLED=0 go test ./...)
	(cd scheduler && CGO_ENABLED=0 go test ./...)
	(cd starters/platform && CGO_ENABLED=0 go test ./...)

build:
	CGO_ENABLED=0 go build ./...
	(cd api && CGO_ENABLED=0 go build ./...)
	(cd config && CGO_ENABLED=0 go build ./...)
	(cd gin && CGO_ENABLED=0 go build ./...)
	(cd observability && CGO_ENABLED=0 go build ./...)
	(cd starters/api && CGO_ENABLED=0 go build ./...)
	(cd starters/worker && CGO_ENABLED=0 go build ./...)
	(cd cli && CGO_ENABLED=0 go build ./...)
	(cd database && CGO_ENABLED=0 go build ./...)
	(cd migration && CGO_ENABLED=0 go build ./...)
	(cd scheduler && CGO_ENABLED=0 go build ./...)
	(cd starters/platform && CGO_ENABLED=0 go build ./...)

run-api:
	HANAMI_API_CONFIG=starters/api/config.yaml go run ./starters/api/cmd/server

run-worker:
	HANAMI_WORKER_CONFIG=starters/worker/config.yaml go run ./starters/worker/cmd/worker

run-platform:
	(cd starters/platform && go run ./cmd/app serve)

run-secured:
	HANAMI_SECURED_DIRECTORY=/tmp go run ./starters/secured/cmd/service

run-reconfigurable:
	go run ./starters/reconfigurable/cmd/service
