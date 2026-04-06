.PHONY: run build test lint docker deploy logs secrets

# Development
run:
	GROQ_API_KEY=$${GROQ_API_KEY} go run .

build:
	go build -o server .

test:
	go test ./... -v -race -count=1

lint:
	go vet ./...

# Docker
docker:
	docker build -t invoiceparser-api:latest .

docker-run: docker
	docker run -p 8080:8080 -e GROQ_API_KEY=$${GROQ_API_KEY} invoiceparser-api:latest

# Fly.io
deploy:
	flyctl deploy

logs:
	flyctl logs

secrets:
	flyctl secrets list

status:
	flyctl status

# Quick health check
ping:
	@curl -s https://invoice-parser-api-gnmr.onrender.com/health | python3 -m json.tool

# Test parse against live API
test-live:
	@echo "Register a key first, then: make parse KEY=inv_xxx FILE=invoice.pdf"

parse:
	curl -s -X POST https://invoice-parser-api-gnmr.onrender.com/v1/parse/invoice \
		-H "X-API-Key: $(KEY)" \
		-F "file=@$(FILE)" | python3 -m json.tool
