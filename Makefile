# This Source Code Form is subject to the terms of the Mozilla Public
# License, v. 2.0. If a copy of the MPL was not distributed with this
# file, You can obtain one at http://mozilla.org/MPL/2.0/.

PROJECT	:= go.mozilla.org/sops/v3
GO 		:= go
GOLINT 	:= golint

GOOS    ?= linux
GOARCH  ?= amd64
OUTPUT  ?= git-sops
RELEASE_VERSION ?= 0.0.0-dev
BUILD_ARGS = --ldflags "-s -X go.mozilla.org/sops/v3/git.Version=$(RELEASE_VERSION)"

build:
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=0 $(GO) build -o $(OUTPUT) $(BUILD_ARGS) ./cmd/sops

all: test vet generate install functional-tests
origin-build: test vet generate install functional-tests-all

install:
	$(GO) install ./cmd/sops

tag: all
	git tag -s $(TAGVER) -a -m "$(TAGMSG)"

lint:
	$(GOLINT) $(PROJECT)

vendor:
	$(GO) mod tidy
	$(GO) mod vendor

vet:
	$(GO) vet $(PROJECT)

test: vendor
	gpg --import pgp/sops_functional_tests_key.asc 2>&1 1>/dev/null || exit 0
	./test.sh

showcoverage: test
	$(GO) tool cover -html=coverage.out

generate: keyservice/keyservice.pb.go
	$(GO) generate

%.pb.go: %.proto
	protoc --go_out=plugins=grpc:. $<

functional-tests:
	$(GO) build -o functional-tests/sops ./cmd/sops
	cd functional-tests && cargo test

# Ignored tests are ones that require external services (e.g. AWS KMS)
# 	TODO: Once `--include-ignored` lands in rust stable, switch to that.
functional-tests-all:
	$(GO) build -o functional-tests/sops go.mozilla.org/sops/v3/cmd/sops
	cd functional-tests && cargo test && cargo test -- --ignored

deb-pkg:
	rm -rf tmppkg
	mkdir -p tmppkg/usr/local/bin
	GOOS=linux CGO_ENABLED=0 go build -o tmppkg/usr/local/bin/git-sops $(BUILD_ARGS) ./cmd/sops
	fpm -C tmppkg -n git-sops --license MPL2.0 --vendor mozilla \
		--description "git-sops is a git helper for mozilla sops." \
		-m "Ivan Andreev <ivandex@gmail.com>" \
		--url https://github.com/ivandeex/git-sops \
		--architecture x86_64 \
		-v "$${RELEASE_NUMBER:-$$(grep '^const Version' version/version.go |cut -d \" -f 2)}" \
		-s dir -t deb .

rpm-pkg:
	rm -rf tmppkg
	mkdir -p tmppkg/usr/local/bin
	GOOS=linux CGO_ENABLED=0 go build -o tmppkg/usr/local/bin/git-sops $(BUILD_ARGS) ./cmd/sops
	fpm -C tmppkg -n git-sops --license MPL2.0 --vendor mozilla \
		--description "git-sops is a git helper for Mozilla SOPS." \
		-m "Ivan Andreev <ivandex@gmail.com>" \
		--url https://github.com/ivandeex/git-sops \
		--architecture x86_64 \
		--rpm-os linux \
		-v "$${RELEASE_NUMBER:-$$(grep '^const Version' version/version.go |cut -d \" -f 2)}" \
		-s dir -t rpm .

dmg-pkg: install
ifneq ($(OS),darwin)
		echo 'you must be on MacOS and set OS=darwin on the make command line to build an OSX package'
else
	rm -rf tmppkg
	mkdir -p tmppkg/usr/local/bin
	cp $$GOPATH/bin/sops tmppkg/usr/local/bin/
	fpm -C tmppkg -n git-sops --license MPL2.0 --vendor mozilla \
		--description "git-sops is a git helper for Mozilla SOPS." \
		-m "Ivan Andreev <ivandex@gmail.com>" \
		--url https://github.com/ivandeex/git-sops \
		--architecture x86_64 \
		-v "$$(grep '^const Version' version/version.go |cut -d \" -f 2)" \
		-s dir -t osxpkg \
		--osxpkg-identifier-prefix org.mozilla.sops \
		-p tmppkg/sops-$$(git describe --abbrev=0 --tags).pkg .
	hdiutil makehybrid -hfs -hfs-volume-name "Mozilla Sops" \
		-o tmppkg/sops-$$(git describe --abbrev=0 --tags).dmg tmpdmg
endif

download-index:
	bash make_download_page.sh

mock:
	go get github.com/vektra/mockery/.../
	mockery -dir vendor/github.com/aws/aws-sdk-go/service/kms/kmsiface/ -name KMSAPI -output kms/mocks

.PHONY: all test generate clean vendor functional-tests mock
