# Production image for Rune: a minimal, non-root, static-binary container.
#
# Rune is pure Go with CGO disabled, so the final image needs no libc, shell, or
# package manager — just the binary on a distroless base. Build:
#
#   docker buildx build -t rune:local --load .
#   docker buildx build --build-arg VERSION=$(git describe --tags) \
#                       --build-arg COMMIT=$(git rev-parse --short HEAD) -t rune:1.2.3 .
#
# See docs/docker.md for usage and the minimal-image limitations.

ARG GO_VERSION=1.27

FROM golang:${GO_VERSION}-bookworm AS build
WORKDIR /src
# Download modules first so they cache independently of source changes.
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=none
ENV CGO_ENABLED=0
RUN go build -trimpath \
        -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
        -o /out/rune ./cmd/rune

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
ARG COMMIT=none
LABEL org.opencontainers.image.title="rune" \
      org.opencontainers.image.description="A shared task runner for humans and AI agents" \
      org.opencontainers.image.source="https://github.com/rune-task-runner/rune" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}"
COPY --from=build /out/rune /rune
WORKDIR /work
ENTRYPOINT ["/rune"]
