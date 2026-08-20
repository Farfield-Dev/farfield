# syntax=docker/dockerfile:1
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X github.com/Farfield-Dev/farfield/internal/buildinfo.Version=${VERSION}" -o /out/farfield ./cmd/farfield

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/farfield /farfield
VOLUME ["/data"]
EXPOSE 8787
ENTRYPOINT ["/farfield"]
CMD ["serve", "--store", "/data", "--listen", "0.0.0.0:8787"]
