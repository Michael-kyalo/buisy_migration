BIN   ?= bin/migrate
IMAGE ?= buisy-migration:latest

.PHONY: build test test-integration vet docker todo clean

build:
	go build -trimpath -o $(BIN) ./cmd/migrate

test:
	go test ./...

# Needs Docker: starts throwaway MySQL 8 and Postgres 16 containers.
test-integration:
	go test -tags integration -count=1 -v ./test/integration/

vet:
	go vet ./...
	go vet -tags integration ./test/...

docker:
	docker build -t $(IMAGE) .

# Lists every open decision and schema assumption in the SQL.
todo:
	@grep -rn "TODO(decision)" transforms validate profile schema || true
	@echo
	@grep -rln "TODO(schema)" transforms validate profile schema || true

clean:
	rm -rf bin
