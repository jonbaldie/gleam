# Gleam

Gleam is a lightweight, configurable reverse proxy with built-in caching capabilities.

## Features

- Reverse proxy functionality
- In-memory or Redis caching for GET requests
- Configurable via environment variables
- Simple and efficient design

## Configuration

Set the following environment variables to configure Gleam:

- `ORIGIN_URL`: Backend server URL (default: https://httpbin.org)
- `TTL_MINUTES`: Cache entry time-to-live in minutes (default: 5)
- `CACHE_SIZE`: Maximum number of cache entries (default: 100)
- `PORT`: Port to run Gleam on (default: 8080)
- `REDIS_URL`: Redis URL (optional, default: redis://localhost:6379/0)
- `CACHE_TYPE`: Set to 'memory' or 'redis' (default: memory)

## Usage

1. Set environment variables as needed
2. Run the application: `go run gleam.go`
3. Access your origin server through Gleam

Gleam will cache GET requests and serve cached responses when available, improving response times and reducing load on your origin server.

## Docker

There is a Docker image maintained as `jonbaldie/gleam`. 

Here's a sample usage, proxying requests to my personal site on port 80:

```bash
docker run --rm -p 80:80 -e ORIGIN_URL=https://www.jonbaldie.com -e PORT=80 jonbaldie/gleam
```

## Redis

This is one of my favourite features of Gleam. By setting `CACHE_TYPE=redis` and `REDIS_URL=redis://{username}:{password}@{hostname}:{port}/{database}` Gleam will cache your HTTP requests in Redis. 

This uses [version 8](https://github.com/redis/go-redis) of `go-redis` so if you are having trouble setting your Redis URL, use their documentation [here](https://github.com/redis/go-redis?tab=readme-ov-file#connecting-via-a-redis-url).

## Naming

I like Varnish but the configuration for it is a nightmare, and much of the documentation is decades old. Gleam intends to take on much of the critical functionality with a much easier configuration experience. 

## Disclaimer

Gleam is intended for an origin server that presents static contents, or static contents determined by the URL accessed. It is not recommend to use Gleam to cache the contents of, say, a logged-in dashboard where the data is dynamic or determined by sessions.  
