ARG GO_IMAGE=docker.arvancloud.ir/library/golang:1.26-alpine
ARG SHIPPING_IMAGE=docker.arvancloud.ir/library/docker:29-cli@sha256:5a7af0dcb0e46c803efe9b45371dd21cb500e5b4453250394ac6f06d49b6e6f1
ARG XRAY_IMAGE=ghcr.io/xtls/xray-core:26.10.31
FROM ${GO_IMAGE} AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-mod=vendor
COPY . .
RUN go build -trimpath -tags timetzdata -ldflags="-s -w" -o /out/manager ./cmd/manager && \
    mkdir -p /out/data && chmod 700 /out/data

FROM scratch AS app
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/manager /manager
COPY --from=build --chown=65532:65532 /out/data /data
COPY web/dist /web
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/manager"]

FROM ${XRAY_IMAGE} AS xray-core

# Xray is supervised by a narrow broker, with no socket, shell or privileged mode.
FROM app AS vpn
COPY --from=xray-core /usr/local/bin/xray /usr/local/bin/xray
EXPOSE 10808 10809
ENTRYPOINT ["/manager", "vpn"]

FROM ${SHIPPING_IMAGE} AS shipping-tools

FROM shipping-tools AS shipping-test-target
RUN apk add --no-cache openssh-server && ssh-keygen -A && \
    mkdir -p /root/.ssh /run/sshd && chmod 700 /root/.ssh && \
    passwd -d root && \
    printf 'PermitRootLogin prohibit-password\nPasswordAuthentication no\n' > /etc/ssh/sshd_config.d/test.conf
ENTRYPOINT ["/usr/sbin/sshd", "-D", "-e"]

FROM shipping-tools AS shipper
COPY --from=build /out/manager /manager
USER 0:0
EXPOSE 8080
ENTRYPOINT ["/manager", "shipper"]

# Resource editor is separate from lifecycle and shipping. No source/key mounts in web.
FROM shipping-tools AS resources
COPY --from=build /out/manager /manager
USER 0:0
EXPOSE 8080
ENTRYPOINT ["/manager", "resources"]

# Local image builds are separate from shipping: Docker socket and read-only source mounts,
# but no SSH keys and no destination access. The CLI image provides git and buildx.
FROM shipping-tools AS builder
COPY --from=build /out/manager /manager
USER 0:0
EXPOSE 8080
ENTRYPOINT ["/manager", "builder"]
