# syntax=docker/dockerfile:1

# ---- Stage 1: builder ----
FROM golang:1.22-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum* ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /app/server .

# ---- Stage 2: runtime ----
FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata wget \
    && adduser -D -u 10001 appuser

WORKDIR /app

COPY --from=builder /app/server /app/server

USER appuser

EXPOSE 8080

ENV PORT=8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://localhost:8080/health || exit 1

CMD ["/app/server"]
