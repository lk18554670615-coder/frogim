# syntax=docker/dockerfile:1.7@sha256:a57df69d0ea827fb7266491f2813635de6f17269be881f696fbfdf2d83dda33e
FROM --platform=$BUILDPLATFORM golang@sha256:d4c4845f5d60c6a974c6000ce58ae079328d03ab7f721a0734277e69905473e5 AS build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
WORKDIR /src
COPY tools/wukong-policy-plugin/go.mod tools/wukong-policy-plugin/go.sum ./
RUN go mod download
COPY tools/wukong-policy-plugin/main.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
      -trimpath -buildvcs=false -ldflags="-s -w -buildid=" \
      -o /out/wk.plugin.im-policy-linux-amd64.wkp . && \
    sha256sum /out/wk.plugin.im-policy-linux-amd64.wkp > /out/wk.plugin.im-policy-linux-amd64.wkp.sha256

FROM alpine@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659
COPY --from=build /out /opt/im-policy
COPY tools/wukong-policy-plugin/install.sh /usr/local/bin/install-im-policy
RUN chmod 0555 /usr/local/bin/install-im-policy
ENTRYPOINT ["/usr/local/bin/install-im-policy"]
