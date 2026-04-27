# syntax=docker/dockerfile:1.7
#
# Production note: before tagging a release, both base images must be
# pinned by SHA256 digest (the @sha256:... form), not by floating tag.
# CI will surface base-image CVEs via trivy; the release workflow's
# pre-tag check refuses to build when either FROM line lacks a digest.
# See docs/operations.md for the pinning procedure.

FROM golang:1.26.2-alpine@sha256:f85330846cde1e57ca9ec309382da3b8e6ae3ab943d2739500e08c86393a21b1 AS builder
WORKDIR /src
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X github.com/mmedum/pipedrive-mcp/internal/version.Version=${VERSION}" \
    -o /out/pipedrive-mcp ./cmd/pipedrive-mcp

FROM gcr.io/distroless/static-debian12:nonroot@sha256:a9329520abc449e3b14d5bc3a6ffae065bdde0f02667fa10880c49b35c109fd1
COPY --from=builder /out/pipedrive-mcp /pipedrive-mcp
USER nonroot:nonroot
ENTRYPOINT ["/pipedrive-mcp"]
