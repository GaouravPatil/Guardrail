# -BUILD-
FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum .
COPY main.go .

# CGO_ENABLED=0 produces a fully static binary — no C library dependencies
# This is what lets us run it in an empty base image below
RUN CGO_ENABLED=0 GOOS=linux go build -o server main.go


# -RUN-
FROM scratch

WORKDIR /app

COPY --from=builder /app/server .

EXPOSE 5000
CMD ["./server"]