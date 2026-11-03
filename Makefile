.PHONY: build test presets lint

build:
	go build -o dist/ragwb ./cmd/ragwb

test:
	go test ./...

# Regression sweep: every prototype preset built and queried headlessly with its evals.
presets: build
	./dist/ragwb presets --dir examples/presets --files examples/apps/reports/corpus

lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	go vet ./...
