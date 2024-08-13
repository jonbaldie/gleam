# Gleam

Gleam is a lightweight, configurable reverse proxy with built-in caching capabilities.

## Features

- Reverse proxy functionality
- In-memory caching for GET requests
- Configurable via environment variables
- Simple and efficient design

## Configuration

Set the following environment variables to configure Gleam:

- `ORIGIN_URL`: Backend server URL (default: https://httpbin.org)
- `TTL_MINUTES`: Cache entry time-to-live in minutes (default: 5)
- `CACHE_SIZE`: Maximum number of cache entries (default: 100)
- `PORT`: Port to run Gleam on (default: 8080)

## Usage

1. Set environment variables as needed
2. Run the application: `go run main.go`
3. Access your origin server through Gleam

Gleam will cache GET requests and serve cached responses when available, improving response times and reducing load on your origin server.
