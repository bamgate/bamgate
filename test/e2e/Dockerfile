# Stage 1: Build bamgate and bamgate-hub binaries.
FROM golang:1.25-bookworm AS builder

WORKDIR /src
COPY go.mod go.sum ./
COPY third_party/ third_party/
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags '-s -w' -o /bamgate ./cmd/bamgate
RUN CGO_ENABLED=0 go build -ldflags '-s -w' -o /bamgate-hub ./cmd/bamgate-hub

# Stage 2: Slim runtime with network tools for testing.
FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    iproute2 iputils-ping procps \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /bamgate /usr/local/bin/bamgate
COPY --from=builder /bamgate-hub /usr/local/bin/bamgate-hub

# Default entrypoint — overridden by docker-compose per service.
ENTRYPOINT ["bamgate"]
