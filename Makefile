## help: show makefile usage
MOCKGENERATE := go run github.com/golang/mock/mockgen@v1.7.0-rc.1

.PHONY: mocks
mocks:
	$(MOCKGENERATE) -source=cipher/cipher.go -destination=mocks/cipher/cipher.go
	$(MOCKGENERATE) -source=cursors/cursor.go -destination=mocks/cursors/cursor.go

test:
	@go test ./...

build:
	cd cmd/steg && go build

## install: install the steg binary to $(GOPATH)/bin (or $GOBIN if set)
.PHONY: install
install:
	go install ./cmd/steg