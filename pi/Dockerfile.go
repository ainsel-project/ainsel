# ainsel-pi variant: Go tooling
#
# Extends the base ainsel-pi image with the Go toolchain and golangci-lint.
# Use this variant when agents need to build, test, or lint Go code.
#
# Build:
#   docker build --build-arg BASE_TAG=main -t ghcr.io/ainsel-project/ainsel-pi-go:go -f pi/Dockerfile.go pi/
#   docker build --build-arg BASE_IMAGE=<registry>/ainsel-pi --build-arg BASE_TAG=dev \
#     -t ainsel-pi-go:local -f pi/Dockerfile.go pi/

ARG BASE_TAG=latest
# The base image this variant extends. CI always passes it explicitly (derived
# from the repo owner), so this default only serves a local `docker build` with
# no arguments. It still points at the Docker Hub mirror because the GHCR
# packages do not exist until the first release from this pipeline; flip the
# default to ghcr.io/ainsel-project/ainsel-pi once they do.
ARG BASE_IMAGE=dpinsel/ainsel-pi
FROM ${BASE_IMAGE}:${BASE_TAG}

USER root

# Install Go toolchain.
# The official Go tarballs are statically linked and run on any Linux
# distro; we just need to pick the right architecture.
ARG GO_VERSION=1.24.2
RUN ARCH=$(dpkg --print-architecture) \
    && case "$ARCH" in \
        amd64) GO_ARCH=amd64 ;; \
        arm64) GO_ARCH=arm64 ;; \
        *) echo "Unsupported architecture: $ARCH"; exit 1 ;; \
    esac \
    && curl -sSL "https://go.dev/dl/go${GO_VERSION}.linux-${GO_ARCH}.tar.gz" \
       | tar -C /usr/local -xzf - \
    && ln -sf /usr/local/go/bin/go /usr/local/bin/go \
    && ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt

# Install golangci-lint — the standard way to run lint on ainsel Go code.
ARG GOLANGCI_LINT_VERSION=v2.8.0
RUN ARCH=$(dpkg --print-architecture) \
    && case "$ARCH" in \
        amd64) GCI_ARCH=linux-amd64 ;; \
        arm64) GCI_ARCH=linux-arm64 ;; \
        *) echo "Unsupported architecture: $ARCH"; exit 1 ;; \
    esac \
    && curl -sSL "https://github.com/golangci/golangci-lint/releases/download/${GOLANGCI_LINT_VERSION}/golangci-lint-${GOLANGCI_LINT_VERSION#v}-${GCI_ARCH}.tar.gz" \
       | tar -C /tmp -xzf - \
    && mv "/tmp/golangci-lint-${GOLANGCI_LINT_VERSION#v}-${GCI_ARCH}/golangci-lint" /usr/local/bin/golangci-lint \
    && rm -rf /tmp/golangci-lint-*

# Re-assert the agent user so the runtime stays non-root.
USER agent

# Inherits ENTRYPOINT from the base image.
