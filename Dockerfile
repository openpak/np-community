# Multi-stage, cross-compiled: the build stage runs on the runner's native arch and Go
# emits the target arch, so linux/arm64 builds need no QEMU.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -mod=vendor -trimpath -ldflags="-s -w" -o /out/np-community ./cmd/np-community

FROM alpine:3.22
LABEL org.opencontainers.image.source="https://github.com/openpak/np-community"
RUN addgroup -S app && adduser -S -G app app && apk add --no-cache ca-certificates
USER app
COPY --from=build /out/np-community /usr/local/bin/np-community
EXPOSE 8003 8443 8523
ENTRYPOINT ["/usr/local/bin/np-community"]
