# -BUILD-
FROM golang:1.27.2-alpine AS builder

WORKDIR /app

# Leverage layer caching: deps first, source last.
COPY go.mod go.sum ./
RUN go mod download

# CI already runs vet + tests — don't repeat them here (keeps image builds fast).
COPY main.go ./

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
