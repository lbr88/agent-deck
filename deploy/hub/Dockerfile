FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS=linux
ARG TARGETARCH
RUN GOOS="${TARGETOS}" GOARCH="${TARGETARCH:-$(go env GOARCH)}" CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" -o /out/agent-deck ./cmd/agent-deck

FROM gcr.io/distroless/base-debian12

COPY --from=build /out/agent-deck /usr/local/bin/agent-deck
EXPOSE 8421
ENTRYPOINT ["/usr/local/bin/agent-deck", "hub", "serve"]
