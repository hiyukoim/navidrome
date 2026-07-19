# Build stage (native platform). For linux/amd64 from Apple Silicon, prefer:
#   GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o lastfm-alias-proxy .
# then use a runtime-only image that COPY that binary.
FROM golang:1.22-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /lastfm-alias-proxy .

# Runtime stage
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY --from=builder /lastfm-alias-proxy /usr/local/bin/lastfm-alias-proxy

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/lastfm-alias-proxy"]
