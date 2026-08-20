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

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/pgav /pgav
USER nonroot:nonroot
ENTRYPOINT ["/pgav"]
CMD ["--help"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=2 \
    CMD ["/pgav", "version"]
