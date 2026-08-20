# syntax=docker/dockerfile:1
# Multi-arch build for the leasereg smoke/health check image.
# The builder matches the locked Go toolchain; the runtime is a static binary
# on alpine. CGO is disabled so modernc.org/sqlite (pure Go) cross-compiles
# cleanly for both linux/amd64 and linux/arm64.
FROM docker.m.daocloud.io/library/golang:1.26.3-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN GOPROXY=https://goproxy.cn,direct GOSUMDB=sum.golang.google.cn GOTOOLCHAIN=local go mod download
COPY . .
RUN CGO_ENABLED=0 GOTOOLCHAIN=local go build -trimpath -o /out/leasereg .

FROM docker.m.daocloud.io/library/alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /out/leasereg /usr/local/bin/leasereg
ENTRYPOINT ["leasereg"]
