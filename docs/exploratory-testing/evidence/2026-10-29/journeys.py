#!/usr/bin/env python3
import http.client
import http.server
import os
import socket
import subprocess
import threading
import time

counts = {}
value = 'initial'

class Origin(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        counts[self.path] = counts.get(self.path, 0) + 1
        body = f'{self.path} value={value} origin-count={counts[self.path]}'.encode()
        self.send_response(200)
        self.send_header('Cache-Control', 'public, max-age=60')
        self.send_header('ETag', '"version-1"')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_PUT(self):
        global value
        value = self.rfile.read(int(self.headers['Content-Length'])).decode()
        self.send_response(204)
        self.end_headers()

server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Origin)
thread = threading.Thread(target=server.serve_forever)
thread.start()
proc = None
try:
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        port = s.getsockname()[1]
    env = dict(os.environ, ORIGIN_URL=f'http://127.0.0.1:{server.server_port}', PORT=str(port), CACHE_TYPE='memory')
    proc = subprocess.Popen(['.scratch/gleam'], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    for _ in range(100):
        try:
            with socket.create_connection(('127.0.0.1', port), timeout=.1):
                break
        except OSError:
            time.sleep(.05)
    else:
        raise RuntimeError('Gleam did not start')

    def request(method, path, headers=None, body=None):
        conn = http.client.HTTPConnection('127.0.0.1', port, timeout=3)
        conn.request(method, path, body=body, headers=headers or {})
        res = conn.getresponse()
        data = res.read().decode()
        result = (res.status, dict(res.getheaders()), data)
        conn.close()
        print(f'{method} {path} {headers or {}} => {res.status} age={result[1].get("Age")} body={data!r}')
        return result

    print('J1: cache hit and query isolation')
    a = request('GET', '/item?color=red')
    b = request('GET', '/item?color=red')
    c = request('GET', '/item?color=blue')
    assert a[2] == b[2] and 'origin-count=1' in c[2]
    assert counts['/item?color=red'] == 1 and counts['/item?color=blue'] == 1
    print('J2: update invalidation and persisted origin state')
    a = request('GET', '/item')
    assert request('PUT', '/item', body='updated')[0] == 204
    b = request('GET', '/item')
    c = request('GET', '/item')
    assert 'value=initial' in a[2] and 'value=updated' in b[2] and b[2] == c[2] and counts['/item'] == 2
    print('J3: validators and request no-cache')
    a = request('GET', '/validator')
    b = request('GET', '/validator', {'If-None-Match': '"version-1"'})
    c = request('GET', '/validator', {'Cache-Control': 'no-cache'})
    assert a[0] == 200 and b[0] == 304 and b[2] == '' and c[0] == 200 and counts['/validator'] == 2
    print('PASS: all assertions; origin counts:', sorted(counts.items()))
finally:
    if proc is not None:
        proc.terminate()
        try:
            proc.wait(timeout=5)
        except subprocess.TimeoutExpired:
            proc.kill()
            proc.wait()
        print(f'cleanup: Gleam pid {proc.pid} exited {proc.returncode}')
    server.shutdown()
    server.server_close()
    thread.join()
    print('cleanup: origin server closed and thread joined')
