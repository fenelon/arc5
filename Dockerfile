# syntax=docker/dockerfile:1

# arc5 is pure Go, so the binary is cross-compiled on the build machine: an
# image for a NAS (linux/amd64 or linux/arm64) builds quickly on a Mac.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/arc5 ./cmd/arc5

# Alpine rather than an empty image: certificates for Telegram and IMAP over
# TLS, time zones for TZ, and wget for the health check.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata \
 && adduser -D -H -u 1000 arc5 \
 && mkdir /data && chown arc5:arc5 /data && chmod 0700 /data
COPY --from=build /out/arc5 /usr/local/bin/arc5
ENV ARC5_LISTEN=:8080 ARC5_DATA=/data
VOLUME /data
EXPOSE 8080
USER arc5
HEALTHCHECK --interval=30s --timeout=5s --start-period=60s \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["arc5"]
CMD ["serve"]
