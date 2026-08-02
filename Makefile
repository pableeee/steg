## help: show makefile usage
MOCKGENERATE := go run github.com/golang/mock/mockgen@v1.7.0-rc.1

.PHONY: mocks
mocks:
	$(MOCKGENERATE) -source=cipher/cipher.go -destination=mocks/cipher/cipher.go
	$(MOCKGENERATE) -source=cursors/cursor.go -destination=mocks/cursors/cursor.go

.PHONY: test
test:
	@go test ./...

## vet: run go vet over hand-written packages
# mocks/ is excluded: gomock's generated recorder methods are named after the
# interface methods they record but return *gomock.Call, which trips vet's
# stdmethods check for any interface method sharing a name with a stdlib one
# (ReadByte, WriteByte). Nothing in the generated code is fixable from here.
.PHONY: vet
vet:
	@go vet $$(go list ./... | grep -v '/mocks/')

build:
	cd cmd/steg && go build

## install: install the steg binary to $(GOPATH)/bin (or $GOBIN if set)
.PHONY: install
install:
	go install ./cmd/steg