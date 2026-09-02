# ClinLang build.
#
# The project is a language and a compiler. There is nothing to bundle: no
# frontend, no assets, no code generation, no dependencies. `go build` is the
# whole build.

GO   := go
DIST := dist
BIN  := clinlang

.PHONY: build install build-all test race fuzz bench check update-golden clean

build:
	$(GO) build -o $(DIST)/$(BIN) ./cmd/clinlang

install:
	$(GO) install ./cmd/clinlang

# Cross-compilation is trivial: no cgo, no dependencies, one static file per
# target.
build-all:
	@mkdir -p $(DIST)
	GOOS=linux   GOARCH=amd64 $(GO) build -o $(DIST)/$(BIN)-linux-amd64       ./cmd/clinlang
	GOOS=linux   GOARCH=arm64 $(GO) build -o $(DIST)/$(BIN)-linux-arm64       ./cmd/clinlang
	GOOS=darwin  GOARCH=amd64 $(GO) build -o $(DIST)/$(BIN)-darwin-amd64      ./cmd/clinlang
	GOOS=darwin  GOARCH=arm64 $(GO) build -o $(DIST)/$(BIN)-darwin-arm64      ./cmd/clinlang
	GOOS=windows GOARCH=amd64 $(GO) build -o $(DIST)/$(BIN)-windows-amd64.exe ./cmd/clinlang

test:
	$(GO) test ./...

# Determinism is the product promise, so the race detector belongs in the
# normal test story rather than being an occasional check.
race:
	$(GO) test -race ./...

# The lexer and parser must never panic or hang on arbitrary input.
fuzz:
	$(GO) test ./pkg/lexer/  -run=Fuzz -fuzz=FuzzLex   -fuzztime=60s
	$(GO) test ./pkg/parser/ -run=Fuzz -fuzz=FuzzParse -fuzztime=60s

bench:
	$(GO) test ./pkg/lexicon/ ./pkg/vocab/ -bench=. -benchmem -run=XXX

check: test
	$(GO) vet ./...
	@test -z "$$(gofmt -l cmd pkg)" || (echo "unformatted files:"; gofmt -l cmd pkg; exit 1)

# Accept intentional changes to golden files, after reviewing the diff.
update-golden:
	$(GO) test ./pkg/backend/  -update
	$(GO) test ./pkg/clinlang/ -update
	$(GO) test ./pkg/sema/     -update

clean:
	rm -rf $(DIST)
