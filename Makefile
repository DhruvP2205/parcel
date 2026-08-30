BINARY := bin/parcel

.PHONY: build test vet clean deps-proof reproducible

build:
	go build -trimpath -buildvcs=false -o $(BINARY) ./cmd/parcel

test:
	go test ./...

vet:
	go vet ./...

deps-proof:
	go list -m all > deps-proof.txt

# Builds the artifact twice into separate paths and confirms the output is
# byte-identical (Reproducible Build bonus). -trimpath strips local
# filesystem paths and -buildvcs=false skips Go's automatic git-metadata
# stamping, so the result doesn't depend on where or when it's built —
# verified by building from two different directories, not just twice in
# the same one. See README.md for a captured example run.
reproducible:
	go build -trimpath -buildvcs=false -o bin/parcel-a ./cmd/parcel
	go build -trimpath -buildvcs=false -o bin/parcel-b ./cmd/parcel
	sha256sum bin/parcel-a bin/parcel-b
	cmp bin/parcel-a bin/parcel-b && echo "byte-identical"

clean:
	rm -rf bin
