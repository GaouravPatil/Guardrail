# -BUILD-
FROM golang:1.23.5-alpine3.21 AS builder

WORKDIR /app

# Leverage layer caching: deps first, source last.
COPY go.mod go.sum ./
RUN go mod download

COPY main.go main_test.go ./
RUN go vet ./... && go test ./...

# Static, stripped, reproducible binary. No C libs -> runs in scratch.
# -trimpath removes local paths from the binary; -s -w strips symbols.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/server main.go

# -RUN-
FROM scratch

WORKDIR /app

# Run as non-root (65532 = distroless-style nonroot UID; scratch has no /etc/passwd
# so Kubernetes runAsNonRoot + numeric UID is what actually enforces it).
COPY --from=builder /app/server /app/server

EXPOSE 5000
USER 65532:65532
ENTRYPOINT ["/app/server"]
