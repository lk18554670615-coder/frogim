FROM docker.io/library/golang@sha256:d4c4845f5d60c6a974c6000ce58ae079328d03ab7f721a0734277e69905473e5 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wukong-probe .

FROM docker.io/library/alpine@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659
RUN apk add --no-cache ca-certificates \
    && addgroup -S -g 10001 probe \
    && adduser -S -D -H -u 10001 -G probe probe
COPY --from=build /out/wukong-probe /wukong-probe
USER probe:probe
ENTRYPOINT ["/wukong-probe"]
