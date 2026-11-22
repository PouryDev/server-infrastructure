FROM docker.arvancloud.ir/library/docker:29-cli@sha256:5a7af0dcb0e46c803efe9b45371dd21cb500e5b4453250394ac6f06d49b6e6f1
RUN apk add --no-cache bash make python3 openssl
EXPOSE 8080
ENTRYPOINT ["python3", "/infra/scripts/storefront-runner.py"]
