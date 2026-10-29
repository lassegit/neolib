.PHONY: start dev stop logs test build fmt vet clean-data

# Start the server in the background. It keeps running after the terminal closes.
start: build
	@if [ -f .neolib.pid ] && kill -0 "$$(cat .neolib.pid)" 2>/dev/null; then \
		echo "neolib is already running (pid $$(cat .neolib.pid))"; \
	else \
		(trap '' HUP; exec ./neolib > neolib.log 2>&1 < /dev/null) & \
		echo $$! > .neolib.pid; \
		echo "neolib started (pid $$(cat .neolib.pid)); logs: neolib.log"; \
	fi

# Start the dev server in the foreground.
dev:
	go run ./cmd/neolib

# Stop the background dev server.
stop:
	@if [ -f .neolib.pid ]; then \
		pid=$$(cat .neolib.pid); \
		if kill -0 "$$pid" 2>/dev/null; then \
			kill "$$pid"; \
			echo "neolib stopped (pid $$pid)"; \
		else \
			echo "neolib is not running"; \
		fi; \
		rm -f .neolib.pid; \
	else \
		echo "neolib is not running (no .neolib.pid)"; \
	fi

logs:
	tail -f neolib.log

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
