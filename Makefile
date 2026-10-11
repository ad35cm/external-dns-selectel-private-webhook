GOLANGCI_VERSION = 2.14.0
LICENCES_IGNORE_LIST = $(shell cat licenses/licenses-ignore-list.txt)

BINARY = external-dns-selectel-private-webhook

PWD = $(shell pwd)
export PATH := $(PWD)/bin:$(PATH)

download:
	go mod download

.PHONY: build
build:
	CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o ./bin/$(BINARY) -v ./cmd/webhook

test:
	go test -race ./...

mocks:
	go generate ./...

GOLANGCI_LINT = bin/golangci-lint-$(GOLANGCI_VERSION)
$(GOLANGCI_LINT):
	curl -sSfL https://golangci-lint.run/install.sh | sh -s -- -b bin v$(GOLANGCI_VERSION)
	@mv bin/golangci-lint "$(@)"

lint: $(GOLANGCI_LINT) download
	$(GOLANGCI_LINT) run -v

out:
	@mkdir -pv "$(@)"

reports:
	@mkdir -pv "$(@)/licenses"

coverage: out
	go test -race ./... -coverprofile=out/cover.out

html-coverage: out/report.json
	go tool cover -html=out/cover.out

.PHONY: out/report.json
out/report.json:
	go test -race ./... -coverprofile=out/cover.out --json | tee "$(@)"

run:
	go run ./cmd/webhook

.PHONY: clean
clean:
	rm -rf ./bin
	rm -rf ./out
	rm -rf ./dist

GO_RELEASER = bin/goreleaser
$(GO_RELEASER):
	GOBIN=$(PWD)/bin go install github.com/goreleaser/goreleaser/v2@latest

.PHONY: release-check
release-check: $(GO_RELEASER) ## Build binaries and both image architectures locally without publishing
	$(GO_RELEASER) check
	$(GO_RELEASER) release --snapshot --clean --skip=publish

GO_LICENSES = bin/go-licenses
$(GO_LICENSES):
	GOBIN=$(PWD)/bin go install github.com/google/go-licenses

.PHONY: license-check
license-check: $(GO_LICENSES) reports ## Check licenses against code.
	$(GO_LICENSES) check --include_tests --ignore $(LICENCES_IGNORE_LIST) ./...

.PHONY: license-report
license-report: $(GO_LICENSES) reports ## Create licenses report against code.
	$(GO_LICENSES) report --include_tests --ignore $(LICENCES_IGNORE_LIST) ./... > ./reports/licenses/licenses-list.csv
