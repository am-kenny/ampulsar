FROM golang:1.27-alpine AS builder

WORKDIR /build

# Cache modules
COPY go.* .
RUN go mod download

COPY . .

# Build a static binary with stripped paths and debug
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-w -s" \
    -o /app/ampulsar \
    ./cmd/bot


RUN mkdir -p /app/data

FROM gcr.io/distroless/static-debian13:nonroot

WORKDIR /app

ENV STORE_PATH=/app/data/store.json


COPY --from=builder --chown=nonroot:nonroot /app/data /app/data
COPY --from=builder /app/ampulsar /app/ampulsar

USER nonroot:nonroot

ENTRYPOINT ["/app/ampulsar"]
