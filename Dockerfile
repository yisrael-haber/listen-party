FROM golang:1.25-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /out/listen-party .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates

RUN addgroup -S listenparty && adduser -S -G listenparty listenparty

ENV XDG_CONFIG_HOME=/data

WORKDIR /app
COPY --from=build /out/listen-party /usr/local/bin/listen-party

RUN mkdir -p /data/listen-party && chown -R listenparty:listenparty /data

USER listenparty

EXPOSE 8080
VOLUME ["/data"]

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null || exit 1

ENTRYPOINT ["listen-party"]
CMD ["-config", "/data/listen-party/config.json"]
