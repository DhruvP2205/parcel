BINARY := bin/parcel

.PHONY: build test vet clean deps-proof

build:
	go build -o $(BINARY) ./cmd/parcel

test:
	go test ./...

vet:
	go vet ./...

deps-proof:
	go list -m all > deps-proof.txt

clean:
	rm -rf bin
