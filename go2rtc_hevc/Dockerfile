# Builds go2rtc from source with the patch in patch/. An image only exists if the patched package's tests pass.
ARG BUILD_FROM

FROM golang:1.26-alpine AS build
ARG GO2RTC_VERSION=1.9.15
RUN apk add --no-cache git
WORKDIR /src
RUN git -c advice.detachedHead=false clone --depth 1 \
        --branch "v${GO2RTC_VERSION}" https://github.com/AlexxIT/go2rtc.git .
COPY patch/aubuilder.go patch/reorder.go patch/depay_test.go pkg/h265/
COPY patch/go2rtc.patch /tmp/go2rtc.patch
RUN git apply /tmp/go2rtc.patch \
    && go test ./pkg/h265/ \
    && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /go2rtc .

FROM ${BUILD_FROM}
COPY --from=build /go2rtc /usr/local/bin/go2rtc
COPY rootfs /
RUN chmod +x /etc/s6-overlay/s6-rc.d/go2rtc/run
