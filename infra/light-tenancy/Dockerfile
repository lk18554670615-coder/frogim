FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
COPY im-server /app/im-server
ENTRYPOINT ["/app/im-server"]
