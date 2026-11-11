# Build the altserv binary in a pinned Go builder and run it as a non-root user.
# Build from the repository root:
#   docker build -f deployments/docker/Dockerfile -t altserv:local .
FROM golang:1.26-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/altserv .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S altserv \
    && adduser -S -G altserv -h /srv altserv

WORKDIR /srv
COPY --from=build /out/altserv /usr/local/bin/altserv
COPY --chown=altserv:altserv start.html /srv/start.html

USER altserv
EXPOSE 8080

# The container is stateless. Readiness needs the database; liveness does not.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/altserv"]
