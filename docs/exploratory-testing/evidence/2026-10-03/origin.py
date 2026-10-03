import http.server
import socketserver
import sys

class ThreadingHTTPServer(socketserver.ThreadingMixIn, http.server.HTTPServer):
    daemon_threads = True

class TestHandler(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        print(f"[ORIGIN] GET {self.path} Host={self.headers.get('Host')}", flush=True)
        if self.path == "/multi-date":
            body = b"multi-date-test"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Date", "Sat, 03 Oct 2026 01:00:00 GMT")
            self.send_header("Date", "Sat, 03 Oct 2026 02:00:00 GMT")
            self.send_header("Cache-Control", "public, max-age=60")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()
        elif self.path == "/multi-last-modified":
            body = b"multi-lm-test"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Last-Modified", "Sat, 03 Oct 2026 01:00:00 GMT")
            self.send_header("Last-Modified", "Sat, 03 Oct 2026 03:00:00 GMT")
            self.send_header("Cache-Control", "public, max-age=300")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()
        elif self.path == "/bare-max-age":
            body = b"bare-max-age"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "public, max-age")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()
        elif self.path == "/bare-s-maxage":
            body = b"bare-s-maxage"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "public, s-maxage")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()
        elif self.path == "/invalid-max-age":
            body = b"invalid-max-age"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "public, max-age=invalid")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()
        elif self.path == "/status-204":
            self.send_response(204)
            self.send_header("Cache-Control", "public, max-age=60")
            self.end_headers()
            self.wfile.flush()
        elif self.path == "/s-maxage-test":
            body = b"s-maxage-wins"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "public, s-maxage=30, max-age=300")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()
        elif self.path == "/initial-age-test":
            body = b"initial-age"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "public, max-age=60")
            self.send_header("Age", "50")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()
        elif self.path == "/empty-200":
            self.send_response(200)
            self.send_header("Cache-Control", "public, max-age=60")
            self.send_header("Content-Length", "0")
            self.end_headers()
            self.wfile.flush()
        elif self.path == "/chunked-no-len":
            self.send_response(200)
            self.send_header("Transfer-Encoding", "chunked")
            self.send_header("Content-Type", "text/plain")
            self.send_header("Cache-Control", "public, max-age=60")
            self.end_headers()
            chunk = b"hello chunked world\n"
            self.wfile.write(f"{len(chunk):X}\r\n".encode() + chunk + b"\r\n0\r\n\r\n")
            self.wfile.flush()
        elif self.path == "/etag-resource":
            body = b"01234567890123456789012345678901234567890123456789" # 50 bytes
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("ETag", '"abc"')
            self.send_header("Cache-Control", "public, max-age=60")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()
        elif self.path == "/redirect-perm":
            self.send_response(301)
            self.send_header("Location", "/new-dest")
            self.send_header("Cache-Control", "public, max-age=3600")
            self.send_header("Content-Length", "0")
            self.end_headers()
            self.wfile.flush()
        elif self.path == "/items/1":
            body = b'{"id": 1, "name": "widget"}'
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "public, max-age=60")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()
        elif self.path == "/query-test?foo=1&bar=2":
            body = b"query-foo-bar"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "public, max-age=60")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()
        elif self.path == "/auth-resource":
            body = b"secret-data"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "private, max-age=60")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()
        else:
            body = b"default-ok"
            self.send_response(200)
            self.send_header("Content-Type", "text/plain")
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "public, max-age=60")
            self.end_headers()
            self.wfile.write(body)
            self.wfile.flush()

    def do_POST(self):
        print(f"[ORIGIN] POST {self.path} Host={self.headers.get('Host')}", flush=True)
        length = int(self.headers.get("Content-Length", 0))
        if length > 0:
            self.rfile.read(length)
        self.send_response(200)
        self.send_header("Content-Length", "0")
        self.end_headers()
        self.wfile.flush()

    def do_PUT(self):
        print(f"[ORIGIN] PUT {self.path} Host={self.headers.get('Host')}", flush=True)
        length = int(self.headers.get("Content-Length", 0))
        if length > 0:
            self.rfile.read(length)
        self.send_response(200)
        self.send_header("Content-Length", "0")
        self.end_headers()
        self.wfile.flush()

    def do_OPTIONS(self):
        print(f"[ORIGIN] OPTIONS {self.path} Host={self.headers.get('Host')}", flush=True)
        self.send_response(204)
        self.send_header("Allow", "GET, POST, OPTIONS")
        self.send_header("Content-Length", "0")
        self.end_headers()
        self.wfile.flush()

port = int(sys.argv[1]) if len(sys.argv) > 1 else 19800
httpd = ThreadingHTTPServer(("127.0.0.1", port), TestHandler)
print(f"Origin listening on {port}", flush=True)
httpd.serve_forever()
