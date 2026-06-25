FROM golang:1.21.4-alpine
WORKDIR /app
COPY . /app
RUN go build
EXPOSE 8080
CMD ["/app/gleam"]
