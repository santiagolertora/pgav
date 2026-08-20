# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/pgav ./cmd/pgav
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/pgav-docker ./cmd/pgav-docker

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/pgav /pgav
COPY --from=builder /out/pgav-docker /pgav-docker
USER nonroot:nonroot
ENTRYPOINT ["/pgav-docker"]
HEALTHCHECK --interval=10s --timeout=3s --start-period=30s --retries=3 \
    CMD ["/pgav", "version"]
