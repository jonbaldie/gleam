import http.server, sys, threading
count = {}
lock = threading.Lock()
class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def log_message(self, fmt, *a): sys.stderr.write("ORIGIN %s %s host=%s\n" % (self.command, self.path, self.headers.get("Host")))
    def _body(self):
        with lock:
            count[self.path] = count.get(self.path, 0) + 1
            n = count[self.path]
        return ("%s hit %d\n" % (self.path, n)).encode()
    def do_GET(self):
        b = self._body()
        self.send_response(200)
        self.send_header("Content-Type", "text/plain")
        self.send_header("Cache-Control", "public, max-age=300")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers(); self.wfile.write(b)
    def do_POST(self):
        l = int(self.headers.get("Content-Length") or 0); self.rfile.read(l)
        self.send_response(204); self.send_header("Content-Length","0"); self.end_headers()
    do_PUT = do_POST
    do_DELETE = do_POST
http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
