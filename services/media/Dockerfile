# Build context is the repository root: media-svc imports the shared pkg/ through go.work.
FROM golang:1.27-alpine3.23 AS builder

WORKDIR /src

COPY go.mod go.sum go.work go.work.sum ./
COPY pkg ./pkg
# go.work lists every service, so all of them must be present for workspace mode.
COPY services ./services

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/media ./services/media/cmd/media

# Alpine's ffmpeg is built with libx264 and the native AAC encoder (both required by the ladder).
FROM alpine:3.23

RUN apk add --no-cache ffmpeg ca-certificates \
    && mkdir -p /var/lib/media

# Alpine already has a /media directory (mount point), so the binary gets its own name.
COPY --from=builder /bin/media /usr/local/bin/media-svc

EXPOSE 1935 8091

ENTRYPOINT ["/usr/local/bin/media-svc"]
