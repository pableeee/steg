## help: show makefile usage
MOCKGENERATE := go run github.com/golang/mock/mockgen@v1.7.0-rc.1

.PHONY: mocks
mocks:
	$(MOCKGENERATE) -source=cipher/cipher.go -destination=mocks/cipher/cipher.go
	$(MOCKGENERATE) -source=cipher/interface.go -destination=mocks/cipher/interface.go
	$(MOCKGENERATE) -source=cursors/cursor.go -destination=mocks/cursors/cursor.go
