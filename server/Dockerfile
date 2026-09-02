FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go run ./cmd/prepare-ipregion -out /out/ipregion
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/im-server ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 im \
    && adduser -S -D -H -u 10001 -G im im
COPY --from=build /out/im-server /im-server
COPY --from=build /out/ipregion /opt/ip2region
ENV IM_IP_REGION_DIR=/opt/ip2region
EXPOSE 8080
USER im:im
ENTRYPOINT ["/im-server"]
