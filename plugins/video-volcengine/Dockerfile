FROM golang:1.24-alpine AS builder

WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /video-plugin-volcengine .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates && addgroup -S plugin && adduser -S -G plugin plugin
RUN mkdir -p /data/tasks && chown -R plugin:plugin /data
COPY --from=builder /video-plugin-volcengine /usr/local/bin/video-plugin-volcengine
USER plugin
ENV VIDEO_PLUGIN_TASK_ROUTE_DIR=/data/tasks
HEALTHCHECK --interval=15s --timeout=5s --start-period=5s --retries=3 CMD wget -q -T 3 -O /dev/null http://localhost:8081/health || exit 1
EXPOSE 8081
ENTRYPOINT ["/usr/local/bin/video-plugin-volcengine"]
