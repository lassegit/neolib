.PHONY: run test build fmt vet clean-data

# Start the dev server.
run:
	go run ./cmd/neolib

# Run the test suite.
test:
	go test ./...

# Compile the server binary.
build:
	go build -o neolib ./cmd/neolib

fmt:
	gofmt -w .

vet:
	go vet ./...

# Delete the local data directory (database and uploaded books). Destructive.
clean-data:
	rm -rf ./data
