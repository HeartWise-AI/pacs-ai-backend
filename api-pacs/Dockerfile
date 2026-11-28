FROM golang:1.26.11

ENV GO111MODULE=on

WORKDIR /app/build
COPY go.mod .
COPY go.sum .

RUN go mod download
COPY . .
RUN go build -o /usr/local/bin/api-pacs ./cmd/main.go

CMD ["/usr/local/bin/api-pacs"]
