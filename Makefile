.PHONY: build test run lint dev seed

build:
	go build -o bin/server ./cmd/server

test:
	go test ./... -v -count=1

run:
	go run ./cmd/server

dev:
	make run & (cd web && npm run dev)

lint:
	go vet ./...
	cd web && npx tsc --noEmit

seed:
	curl -s -X POST http://localhost:8080/api/schema/seed | jq .
