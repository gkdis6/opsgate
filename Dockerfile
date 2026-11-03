# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27@sha256:2920bb124f1a2e12a1d39f0cab756ae0068441ee1da7655dec7f5e374944095b AS builder
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
    -ldflags "-s -w -X github.com/gkdis6/opsgate/internal/buildinfo.version=$VERSION" \
    -o /out/opsgate ./cmd/opsgate
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH sh scripts/third-party-licenses.sh

# GoReleaser supplies platform-specific binaries in its temporary context.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS release
ARG TARGETPLATFORM
COPY --chmod=0555 ${TARGETPLATFORM}/opsgate /opsgate
COPY dist/third_party/ /licenses/
USER nonroot
ENTRYPOINT ["/opsgate"]

# The default target builds from source for local use.
FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab AS local
COPY --from=builder --chmod=0555 /out/opsgate /opsgate
COPY --from=builder /src/dist/third_party/ /licenses/
USER nonroot
ENTRYPOINT ["/opsgate"]
