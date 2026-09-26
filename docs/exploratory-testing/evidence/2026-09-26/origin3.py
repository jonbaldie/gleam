import http.server, sys, gzip, threading
count = {}
lock = threading.Lock()
PAGE = b"<html>" + b"x"*5000 + b"</html>\n"
class H(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"
    def log_message(self, fmt, *a): sys.stderr.write("ORIGIN %s %s host=%s ae=%s inm=%s\n" % (self.command, self.path, self.headers.get("Host"), self.headers.get("Accept-Encoding"), self.headers.get("If-None-Match")))
    def bump(self):
        with lock:
            count[self.path] = count.get(self.path, 0) + 1
            return count[self.path]
    def send(self, code, hdrs, body):
        self.send_response(code)
        for k, v in hdrs: self.send_header(k, v)
        if body is not None: self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        if body and self.command != "HEAD": self.wfile.write(body)
    def do_GET(self):
        n = self.bump()
        p = self.path
        if p.startswith("/gz"):
            body = PAGE; h = [("Content-Type","text/html"),("Cache-Control","public, max-age=300"),("Vary","Accept-Encoding"),("X-Origin-Hit",str(n))]
            if "gzip" in (self.headers.get("Accept-Encoding") or ""):
                body = gzip.compress(PAGE); h.append(("Content-Encoding","gzip"))
            return self.send(200, h, body)
        if p.startswith("/etag"):
            h = [("Content-Type","text/plain"),("Cache-Control","public, max-age=300"),("ETag",'"v1"'),("X-Origin-Hit",str(n))]
            if self.headers.get("If-None-Match") == '"v1"':
                return self.send(304, h, None)
            return self.send(200, h, b"etag body\n")
        if p.startswith("/big"):
            body = b"a" * (64*1024*1024)
            return self.send(200, [("Content-Type","application/octet-stream"),("Cache-Control","public, max-age=300")], body)
        body = ("%s hit %d\n" % (p, n)).encode()
        return self.send(200, [("Content-Type","text/plain"),("Cache-Control","public, max-age=300"),("X-Origin-Hit",str(n))], body)
    do_HEAD = do_GET
http.server.ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
