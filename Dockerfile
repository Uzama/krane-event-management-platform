# cmd/api's image. Pinned to the exact Go patch go.mod requires (go 1.23.0),
# on the same Alpine minor as the runtime stage below -- same principle as the
# pinned images in docker-compose.yml: local and CI build byte-identical
# binaries rather than whatever "latest" resolves to on the day.
FROM golang:1.23.12-alpine3.22 AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY cmd/api ./cmd/api
COPY internal ./internal

RUN CGO_ENABLED=0 go build -o /out/api ./cmd/api

# Runtime. Alpine rather than distroless/scratch: this is a 15-hour
# assignment, not a hardened prod image, and a shell is occasionally useful
# for `docker compose exec api sh` debugging. ca-certificates is included for
# item 06's JWKS fetch against a hosted OIDC issuer.
FROM alpine:3.22.5

RUN apk add --no-cache ca-certificates \
	&& adduser -D -u 10001 krane
USER krane

COPY --from=builder /out/api /usr/local/bin/api

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/api"]
