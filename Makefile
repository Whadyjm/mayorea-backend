.PHONY: run run-mem test vet fmt tidy build up down

run:      ## API against Postgres (needs DATABASE_URL)
	go run ./cmd/api

run-mem:  ## API with in-memory storage, no database needed (dev owner: dueno@mayorea.local / cambiame123)
	STORAGE=memory BOOTSTRAP_OWNER_EMAIL=dueno@mayorea.local BOOTSTRAP_OWNER_PASSWORD=cambiame123 go run ./cmd/api

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -l -w .

tidy:
	go mod tidy

build:
	go build -o bin/api ./cmd/api

up:       ## local Postgres
	docker compose up -d db

down:
	docker compose down
