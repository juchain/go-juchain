# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: infi-linux infi-linux-386 infi-linux-amd64 infi-linux-mips64 infi-linux-mips64le
.PHONY: infi-linux-arm infi-linux-arm-5 infi-linux-arm-6 infi-linux-arm-7 infi-linux-arm64
.PHONY: infi-darwin infi-darwin-386 infi-darwin-amd64
.PHONY: infi-windows infi-windows-386 infi-windows-amd64

GOBIN = $(shell pwd)/build/bin
GO ?= latest

all:
	./env.sh go run build.go install

test: all
	./env.sh go run build.go test

clean:
	rm -fr build/_workspace/pkg/ $(GOBIN)/*

# The devtools target installs tools required for 'go generate'.
# You need to put $GOBIN (or $GOPATH/bin) in your PATH to use 'go generate'.

devtools:
	env GOBIN= go get -u golang.org/x/tools/cmd/stringer
	env GOBIN= go get -u github.com/jteeuwen/go-bindata/go-bindata
	env GOBIN= go get -u github.com/fjl/gencodec
	env GOBIN= go install ./cmd/abigen

# Cross Compilation Targets (xgo)

infi-cross: infi-linux infi-darwin infi-windows infi-android infi-ios
	@echo "Full cross compilation done:"
	@ls -ld $(GOBIN)/infi-*

infi-linux: infi-linux-386 infi-linux-amd64 infi-linux-arm infi-linux-mips64 infi-linux-mips64le
	@echo "Linux cross compilation done:"
	@ls -ld $(GOBIN)/infi-linux-*

infi-linux-386:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/386 -v ./cmd/infi
	@echo "Linux 386 cross compilation done:"
	@ls -ld $(GOBIN)/infi-linux-* | grep 386

infi-linux-amd64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/amd64 -v ./cmd/infi
	@echo "Linux amd64 cross compilation done:"
	@ls -ld $(GOBIN)/infi-linux-* | grep amd64

infi-linux-arm: infi-linux-arm-5 infi-linux-arm-6 infi-linux-arm-7 infi-linux-arm64
	@echo "Linux ARM cross compilation done:"
	@ls -ld $(GOBIN)/infi-linux-* | grep arm

infi-linux-arm-5:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm-5 -v ./cmd/infi
	@echo "Linux ARMv5 cross compilation done:"
	@ls -ld $(GOBIN)/infi-linux-* | grep arm-5

infi-linux-arm-6:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm-6 -v ./cmd/infi
	@echo "Linux ARMv6 cross compilation done:"
	@ls -ld $(GOBIN)/infi-linux-* | grep arm-6

infi-linux-arm-7:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm-7 -v ./cmd/infi
	@echo "Linux ARMv7 cross compilation done:"
	@ls -ld $(GOBIN)/infi-linux-* | grep arm-7

infi-linux-arm64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/arm64 -v ./cmd/infi
	@echo "Linux ARM64 cross compilation done:"
	@ls -ld $(GOBIN)/infi-linux-* | grep arm64

infi-linux-mips:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mips --ldflags '-extldflags "-static"' -v ./cmd/infi
	@echo "Linux MIPS cross compilation done:"
	@ls -ld $(GOBIN)/infi-linux-* | grep mips

infi-linux-mipsle:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mipsle --ldflags '-extldflags "-static"' -v ./cmd/infi
	@echo "Linux MIPSle cross compilation done:"
	@ls -ld $(GOBIN)/infi-linux-* | grep mipsle

infi-linux-mips64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mips64 --ldflags '-extldflags "-static"' -v ./cmd/infi
	@echo "Linux MIPS64 cross compilation done:"
	@ls -ld $(GOBIN)/infi-linux-* | grep mips64

infi-linux-mips64le:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=linux/mips64le --ldflags '-extldflags "-static"' -v ./cmd/infi
	@echo "Linux MIPS64le cross compilation done:"
	@ls -ld $(GOBIN)/infi-linux-* | grep mips64le

infi-darwin: infi-darwin-386 infi-darwin-amd64
	@echo "Darwin cross compilation done:"
	@ls -ld $(GOBIN)/infi-darwin-*

infi-darwin-386:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=darwin/386 -v ./cmd/infi
	@echo "Darwin 386 cross compilation done:"
	@ls -ld $(GOBIN)/infi-darwin-* | grep 386

infi-darwin-amd64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=darwin/amd64 -v ./cmd/infi
	@echo "Darwin amd64 cross compilation done:"
	@ls -ld $(GOBIN)/infi-darwin-* | grep amd64

infi-windows: infi-windows-386 infi-windows-amd64
	@echo "Windows cross compilation done:"
	@ls -ld $(GOBIN)/infi-windows-*

infi-windows-386:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=windows/386 -v ./cmd/infi
	@echo "Windows 386 cross compilation done:"
	@ls -ld $(GOBIN)/infi-windows-* | grep 386

infi-windows-amd64:
	build/env.sh go run build/ci.go xgo -- --go=$(GO) --targets=windows/amd64 -v ./cmd/infi
	@echo "Windows amd64 cross compilation done:"
	@ls -ld $(GOBIN)/infi-windows-* | grep amd64
