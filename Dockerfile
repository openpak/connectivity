FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    GOWORK=off go build -trimpath -buildvcs=false -o /out/connectivityd ./cmd/connectivityd

FROM alpine:3.23
RUN adduser -D -u 65532 -h /app app
WORKDIR /app
COPY --from=build /out/connectivityd /app/connectivityd
USER 65532:65532
ENTRYPOINT ["/app/connectivityd"]
