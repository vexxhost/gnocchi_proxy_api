FROM golang:1.26.0 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/gnocchi-proxy-api ./cmd/gnocchi-proxy-api

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app

COPY --from=build /out/gnocchi-proxy-api /usr/local/bin/gnocchi-proxy-api

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/gnocchi-proxy-api"]
