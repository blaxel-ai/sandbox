"""Smoke test for the ``urllib3`` bump to 2.8.0 (GHSA-vxq7-64xx-v4gw,
GHSA-8988-9cw3-xx77, GHSA-gh4c-6fx4-qh6g).

urllib3 is reached through ``requests`` (pinned in both requirements files),
which is how server code and notebook cells use it. The test makes a real
``requests`` round-trip over a loopback socket with a chunked response, the
transfer mode the chunk-size-line advisory is about, and streams it back.
"""

import http.server
import threading

import requests
import urllib3


class _Chunked(http.server.BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def do_GET(self):
        self.send_response(200)
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()
        for part in (b"hello ", b"from ", b"urllib3"):
            self.wfile.write(b"%x\r\n%s\r\n" % (len(part), part))
        self.wfile.write(b"0\r\n\r\n")

    def log_message(self, *args):
        pass


def test_requests_streams_a_chunked_response():
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), _Chunked)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        with requests.get(f"http://127.0.0.1:{server.server_address[1]}/", stream=True, timeout=10) as response:
            assert response.status_code == 200
            assert b"".join(response.iter_content(chunk_size=4)) == b"hello from urllib3"
    finally:
        server.shutdown()
    major, minor = (int(x) for x in urllib3.__version__.split(".")[:2])
    assert (major, minor) >= (2, 8), urllib3.__version__
