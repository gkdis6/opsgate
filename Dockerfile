# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY scripts/third-party-licenses.sh ./scripts/
ARG TARGETOS=linux
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/gkdis6/opsgate/internal/server.Version=$VERSION" \
    -o /out/opsgate ./cmd/opsgate
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH sh scripts/third-party-licenses.sh

# GoReleaser supplies platform-specific binaries in its temporary context.
FROM gcr.io/distroless/static-debian12:nonroot AS release
ARG TARGETPLATFORM
COPY --chmod=0555 ${TARGETPLATFORM}/opsgate /opsgate
COPY dist/third_party/ /licenses/
USER nonroot
ENTRYPOINT ["/opsgate"]

# The default target builds from source for local use.
FROM gcr.io/distroless/static-debian12:nonroot AS local
COPY --from=builder --chmod=0555 /out/opsgate /opsgate
COPY --from=builder /src/dist/third_party/ /licenses/
USER nonroot
ENTRYPOINT ["/opsgate"]
