import http.server, sys
BODY = b"s" * (4*1024*1024)
class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def log_message(self, *a): pass
    def do_GET(self):
        self.send_response(200)
        self.send_header("Cache-Control", "public, max-age=2")
        self.send_header("Content-Length", str(len(BODY)))
        self.end_headers(); self.wfile.write(BODY)
http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
