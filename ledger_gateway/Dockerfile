FROM golang:1.23-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY gateway.go ./
RUN CGO_ENABLED=0 go build -o gateway gateway.go

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=build /app/gateway /usr/local/bin/gateway
CMD ["gateway"]
