# Build context is the repository root: billing-svc imports the shared pkg/ through go.work.
FROM golang:1.27-alpine3.23 AS builder

WORKDIR /src

COPY go.mod go.sum go.work go.work.sum ./
COPY pkg ./pkg
# go.work lists every service, so all of them must be present for workspace mode.
COPY services ./services

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /bin/billing ./services/billing/cmd/billing

FROM scratch

COPY --from=builder /bin/billing /billing
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

EXPOSE 8098

ENTRYPOINT ["/billing"]
