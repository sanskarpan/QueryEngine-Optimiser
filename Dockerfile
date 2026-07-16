# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /app/bin/server ./cmd/server

# Runtime stage — minimal image with no Go toolchain
FROM alpine:3.20

RUN addgroup -S app && adduser -S app -G app

WORKDIR /app

COPY --from=builder /app/bin/server /app/server

USER app

EXPOSE 8080

ENV PORT=8080
ENV CORS_ORIGIN=*

ENTRYPOINT ["/app/server"]
