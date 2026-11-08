# The release version lives in the VERSION file; override with VERSION=...
VERSION ?= $(shell cat VERSION 2>/dev/null || echo 0.0.0)
LDFLAGS := -s -w -X main.version=$(VERSION)

# Let go fill in any go.sum entries missing for this platform.
export GOFLAGS ?= -mod=mod
# Package architecture follows the Go toolchain (amd64, arm64).
export DRIFT_ARCH ?= $(shell go env GOARCH)
PREFIX  ?= /usr/local

.PHONY: all build test vet deps install uninstall deb deb-native rpm snapshots clean

all: build

build:
	mkdir -p bin
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o bin/drift ./cmd/drift
	go build -trimpath -ldflags '$(LDFLAGS)' -o bin/drift-render ./cmd/drift-render

test:
	go test ./internal/conf ./internal/catalog ./internal/idle

vet:
	go vet ./...

# Build-time packages for drift-render (the GLFW window backend is cgo).
deps:
	@if command -v apt-get >/dev/null; then \
	  sudo apt-get install -y golang gcc pkg-config libgl1-mesa-dev libx11-dev libxrandr-dev \
	    libxcursor-dev libxinerama-dev libxi-dev libxxf86vm-dev; \
	elif command -v dnf >/dev/null; then \
	  sudo dnf install -y golang gcc pkgconf-pkg-config mesa-libGL-devel libX11-devel libXrandr-devel \
	    libXcursor-devel libXinerama-devel libXi-devel libXxf86vm-devel; \
	fi

install: build
	install -Dm755 bin/drift        $(DESTDIR)$(PREFIX)/bin/drift
	install -Dm755 bin/drift-render $(DESTDIR)$(PREFIX)/bin/drift-render
	install -Dm644 packaging/drift.service $(DESTDIR)$(HOME)/.config/systemd/user/drift.service
	sed -i 's#/usr/bin/drift#$(PREFIX)/bin/drift#' $(DESTDIR)$(HOME)/.config/systemd/user/drift.service
	@echo; echo "now: systemctl --user daemon-reload && systemctl --user enable --now drift"

uninstall:
	-systemctl --user disable --now drift.service
	rm -f $(DESTDIR)$(PREFIX)/bin/drift $(DESTDIR)$(PREFIX)/bin/drift-render
	rm -f $(DESTDIR)$(HOME)/.config/systemd/user/drift.service

deb: build
	mkdir -p dist
	DRIFT_VERSION=$(VERSION) nfpm package -p deb -t dist/

# .deb without nfpm (Debian/Ubuntu hosts only)
deb-native: build
	packaging/mkdeb.sh $(VERSION)

rpm: build
	mkdir -p dist
	DRIFT_VERSION=$(VERSION) nfpm package -p rpm -t dist/

# Render a frame of every saver into snapshots/ (needs a display).
snapshots: build
	mkdir -p snapshots
	for s in $$(./bin/drift list 2>/dev/null | awk 'NR>1 && $$2!="video" {print $$1}'); do \
	  ./bin/drift snapshot $$s snapshots/$$s.png 6 || true; done

clean:
	rm -rf bin dist snapshots
