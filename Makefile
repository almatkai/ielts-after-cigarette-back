.PHONY: run test lint build migrate-up migrate-down compose-up compose-down

run:
	go run ./cmd/api

test:
	go test ./...

lint:
	go fmt ./...
	go vet ./...

build:
	go build ./...

migrate-up:
	docker compose run --rm migrate -path=/migrations -database "$$DATABASE_URL" up

migrate-down:
	docker compose run --rm migrate -path=/migrations -database "$$DATABASE_URL" down 1

compose-up:
	docker compose up --build

compose-down:
	docker compose down

