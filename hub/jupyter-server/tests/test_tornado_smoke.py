"""Smoke tests for the ``tornado`` bump to 6.5.9 (GHSA-c2m8-h5v5-343r,
GHSA-chx6-46f5-w4vp, GHSA-3hv7-mjh2-fv65).

jupyter-server is a tornado application and serves its static assets through
``tornado.web.StaticFileHandler``, so this test builds a real tornado
``Application`` with that handler, binds it to a loopback socket and fetches
through tornado's own ``AsyncHTTPClient``.

GHSA-c2m8-h5v5-343r: StaticFileHandler followed a symlink that pointed outside
the static root. The second test plants such a symlink and checks that the
target file is no longer served.
"""

import asyncio
import os

import pytest
import tornado.httpclient
import tornado.httpserver
import tornado.testing
import tornado.web


async def _fetch(static_root, path):
    app = tornado.web.Application([(r"/static/(.*)", tornado.web.StaticFileHandler, {"path": static_root})])
    sock, port = tornado.testing.bind_unused_port()
    server = tornado.httpserver.HTTPServer(app)
    server.add_sockets([sock])
    try:
        client = tornado.httpclient.AsyncHTTPClient()
        response = await client.fetch(f"http://127.0.0.1:{port}/static/{path}", raise_error=False)
        return response.code, response.body
    finally:
        server.stop()


def test_static_file_round_trip(tmp_path):
    root = tmp_path / "static"
    root.mkdir()
    (root / "hello.txt").write_bytes(b"hello from tornado")
    code, body = asyncio.run(_fetch(str(root), "hello.txt"))
    assert code == 200
    assert body == b"hello from tornado"


def test_symlink_outside_static_root_is_not_served(tmp_path):
    root = tmp_path / "static"
    root.mkdir()
    secret = tmp_path / "secret.txt"
    secret.write_bytes(b"outside the static root")
    os.symlink(secret, root / "leak.txt")
    code, body = asyncio.run(_fetch(str(root), "leak.txt"))
    assert code in (403, 404), (code, body)
    assert b"outside the static root" not in body
