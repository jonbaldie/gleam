FROM golang:1.21.4-alpine
WORKDIR /app
COPY . /app
RUN go build
CMD ["/app/gleam"]
