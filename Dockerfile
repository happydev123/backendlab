# syntax=docker/dockerfile:1

# Build stage: static binaries, no cgo.
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=0.1.0-dev
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/happydev123/backendlab/internal/version.Version=${VERSION}" \
      -o /out/ ./cmd/backendlab ./cmd/mockservice

# Runtime stage: distroless static image with a non-root user. The image
# carries CA certificates for TLS verification and nothing else.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/backendlab /usr/local/bin/backendlab
COPY --from=build /out/mockservice /usr/local/bin/mockservice
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/backendlab"]
CMD ["serve", "--config", "/etc/backendlab/backendlab-server.yaml"]
