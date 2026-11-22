ARG GO_IMAGE=docker.arvancloud.ir/library/golang:1.26-alpine
FROM ${GO_IMAGE} AS build
WORKDIR /src
ENV CGO_ENABLED=0 GOFLAGS=-trimpath
COPY . .
# `make image` vendors the modules first, so this build needs no network. Without vendor/ the modules
# are downloaded (set GOPROXY with --build-arg if your network needs a mirror).
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}
RUN if [ -d vendor ]; then export GOFLAGS="$GOFLAGS -mod=vendor"; fi; \
    go build -tags timetzdata -ldflags="-s -w" -o /out/api ./cmd/api && \
    go build -tags timetzdata -ldflags="-s -w" -o /out/migrate ./cmd/migrate && \
    go build -tags timetzdata -ldflags="-s -w" -o /out/user ./cmd/user && \
    go build -tags timetzdata -ldflags="-s -w" -o /out/seed ./cmd/seed && \
    echo "app:x:65532:65532::/:/sbin/nologin" > /out/passwd

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/passwd /etc/passwd
COPY --from=build /out/api /api
COPY --from=build /out/migrate /migrate
COPY --from=build /out/user /user
COPY --from=build /out/seed /seed
# The built accounting UI (scripts/mizan-host.sh builds mizan-ui and copies dist/ here); served at / when WEB_DIR=/web.
COPY --from=build /src/web /web
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/api"]
