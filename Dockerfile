# syntax=docker/dockerfile:1.7

# One image, both programs: the Dumbo channel and the Dumbo factory work
# only together. No GOARCH: the build stage runs as the target platform
# (QEMU in CI), so the binaries always match the image's architecture.
FROM golang:1.25-alpine AS builder
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
# VERSION is reported to core at registration, and goes into the pages' hello.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /out/dumbo-channel ./cmd/dumbo-channel && \
    CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /out/dumbo-factory ./cmd/dumbo-factory

# The page is a template built into the factory: no toolchain at run time.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=builder /out/dumbo-channel /app/dumbo-channel
COPY --from=builder /out/dumbo-factory /app/dumbo-factory
# Both are configured entirely from the environment (internal/config for
# the channel, cmd/dumbo-factory/main.go for the factory). No port: they
# only dial out (to core, its RabbitMQ, and pages).

ENTRYPOINT ["/app/dumbo-channel"]
