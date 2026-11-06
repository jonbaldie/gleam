#!/usr/bin/env python3
import errno
import http.client
import http.server
import os
import signal
import socket
import subprocess
import threading
import time

counts = {}


class Origin(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_GET(self):
        counts[self.path] = counts.get(self.path, 0) + 1
        body = f'{self.path} count={counts[self.path]} cookie={self.headers.get("Cookie", "-")}'.encode()
        self.send_response(200)
        if self.path == '/auth-public':
            self.send_header('Cache-Control', 'public, max-age=60')
        elif self.path != '/auth-private':
            self.send_header('Cache-Control', 'max-age=60')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)


def run():
    server = None
    thread = None
    proc = None
    origin_port = None
    port = None
    try:
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Origin)
        origin_port = server.server_port
        thread = threading.Thread(target=server.serve_forever)
        thread.start()
        with socket.socket() as s:
            s.bind(('127.0.0.1', 0))
            port = s.getsockname()[1]
        env = dict(os.environ, ORIGIN_URL=f'http://127.0.0.1:{origin_port}', PORT=str(port), CACHE_TYPE='memory')
        proc = subprocess.Popen(['.scratch/gleam'], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        print(f'resources: origin port={origin_port}, proxy port={port}, proxy pid={proc.pid}', flush=True)
        for _ in range(100):
            try:
                with socket.create_connection(('127.0.0.1', port), timeout=.1):
                    break
            except OSError:
                time.sleep(.05)
        else:
            raise RuntimeError('proxy did not start')

        def get(path, headers=None):
            conn = http.client.HTTPConnection('127.0.0.1', port, timeout=3)
            try:
                conn.request('GET', path, headers=headers or {})
                res = conn.getresponse()
                body = res.read().decode()
                result = (res.status, body)
                print(f'GET {path} {headers or {}} => {res.status} age={res.getheader("Age")} body={body!r}', flush=True)
                return result
            finally:
                conn.close()

        print('J1: authenticated requests with and without shared-cache permission')
        auth = {'Authorization': 'Bearer test'}
        assert get('/auth-private', auth) == (200, '/auth-private count=1 cookie=-')
        assert get('/auth-private', auth) == (200, '/auth-private count=2 cookie=-')
        assert get('/auth-public', auth) == (200, '/auth-public count=1 cookie=-')
        assert get('/auth-public', auth) == (200, '/auth-public count=1 cookie=-')
        assert counts['/auth-private'] == 2 and counts['/auth-public'] == 1

        print('J2: default cookie key isolation and repeat hits')
        assert get('/cookie', {'Cookie': 'session=red'}) == (200, '/cookie count=1 cookie=session=red')
        assert get('/cookie', {'Cookie': 'session=red'}) == (200, '/cookie count=1 cookie=session=red')
        assert get('/cookie', {'Cookie': 'session=blue'}) == (200, '/cookie count=2 cookie=session=blue')
        assert get('/cookie', {'Cookie': 'session=blue'}) == (200, '/cookie count=2 cookie=session=blue')
        assert counts['/cookie'] == 2

        print('J3: offline-only requests on a miss and a hit')
        only = {'Cache-Control': 'only-if-cached'}
        assert get('/offline', only)[0] == 504
        assert '/offline' not in counts
        assert get('/offline') == (200, '/offline count=1 cookie=-')
        assert get('/offline', only) == (200, '/offline count=1 cookie=-')
        assert counts['/offline'] == 1
        print('PASS: all assertions; origin counts:', sorted(counts.items()))
    finally:
        if proc is not None:
            proc.terminate()
            try:
                proc.wait(timeout=5)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
            print(f'cleanup: proxy pid={proc.pid} exited={proc.returncode}')
        if server is not None:
            if thread is not None and thread.is_alive():
                server.shutdown()
                thread.join()
            server.server_close()
            print('cleanup: origin server closed and thread joined')
        for name, checked_port in [('origin', origin_port), ('proxy', port)]:
            if checked_port is not None:
                with socket.socket() as check:
                    check.settimeout(1)
                    result = check.connect_ex(('127.0.0.1', checked_port))
                print(f'cleanup: {name} port={checked_port} connect errno={result}')
                if result != errno.ECONNREFUSED:
                    raise RuntimeError(f'{name} port {checked_port} teardown not verified: errno {result}')


if __name__ == '__main__':
    def stop_on_signal(signum, frame):
        raise KeyboardInterrupt(f'signal {signum}')

    signal.signal(signal.SIGTERM, stop_on_signal)
    run()
