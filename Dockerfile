FROM golang:1.26.0@sha256:fb612b7831d53a89cbc0aaa7855b69ad7b0caf603715860cf538df854d047b84 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/gnocchi-proxy-api ./cmd/gnocchi-proxy-api

FROM gcr.io/distroless/static-debian12:nonroot@sha256:d093aa3e30dbadd3efe1310db061a14da60299baff8450a17fe0ccc514a16639

WORKDIR /app

COPY --from=build /out/gnocchi-proxy-api /usr/local/bin/gnocchi-proxy-api

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/gnocchi-proxy-api"]
