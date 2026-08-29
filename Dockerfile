FROM golang:1.27-alpine AS builder

WORKDIR /app

# Cache modules
COPY go.* .
RUN go mod download

COPY . .

# Build a static binary with stripped paths and debug 
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags="-w -s" \
    -o /bin/ampulsar \
    ./cmd/bot


FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=builder /bin/ampulsar /bin/ampulsar

USER nonroot:nonroot

ENTRYPOINT ["/bin/ampulsar"]
