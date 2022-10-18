VERSION := "0.0.3"
DATETIME := $(shell date +%Y-%m-%dT%H:%M:%S%z)
LDFLAGS := "-s -w -X main.Version=${VERSION} -X main.UpdatedAt=${DATETIME}"

.PHONY: clean build publish

build: clean
	cd cmd/deployman && GOOS=linux GOARCH=amd64 go build -ldflags ${LDFLAGS} -trimpath -o ../../.bin/linux_amd64/deployman
	cd cmd/deployman && GOOS=windows GOARCH=amd64 go build -ldflags ${LDFLAGS} -trimpath -o ../../.bin/windows_amd64/deployman
	cd cmd/deployman && GOOS=darwin GOARCH=arm64 go build -ldflags ${LDFLAGS} -trimpath -o ../../.bin/darwin_arm64/deployman

clean:
	rm -rf .bin

