FROM golang:1.26.6-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
    go build -buildvcs=false -trimpath -ldflags="-s -w" \
    -o /out/maiden-lane ./cmd/maiden-lane

FROM scratch

# The current server has no outbound calls, but copying the standard CA bundle
# keeps the minimal runtime ready for future TLS clients without adding a shell
# or package manager.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=65532:65532 /out/maiden-lane /maiden-lane

USER 65532:65532
EXPOSE 8080

ENTRYPOINT ["/maiden-lane"]
CMD ["serve", "--listen-address=:8080"]
