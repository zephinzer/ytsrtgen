# syntax=docker/dockerfile:1.7

FROM golang:1.25-alpine AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-mod=readonly
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download -x
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags="-s -w" -o /out/ytsrtgen .

FROM alpine:3.22.4 AS runtime

RUN apk add --no-cache \
        python3 \
        py3-pip \
        ca-certificates \
        tini \
    && addgroup -S app \
    && adduser -S -G app -h /home/app -s /sbin/nologin app \
    && install -d -o app -g app /opt/venv

USER app:app
RUN python3 -m venv /opt/venv \
    && /opt/venv/bin/pip install --no-cache-dir --upgrade pip \
    && /opt/venv/bin/pip install --no-cache-dir -U yt-dlp bgutil-ytdlp-pot-provider

ENV PATH="/opt/venv/bin:${PATH}" \
    LISTEN_ADDR=":8080" \
    TMPDIR=/tmp

COPY --from=build --chown=root:root --chmod=0555 /out/ytsrtgen /usr/local/bin/ytsrtgen

WORKDIR /home/app
EXPOSE 8080

ENTRYPOINT ["/sbin/tini", "--", "/usr/local/bin/ytsrtgen"]
