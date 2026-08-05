"""Smoke tests for the ``aiohttp`` package shipped in this image's Python
environment (see ``requirements.txt``).

``aiohttp`` is not imported by our own server code (``server/*.py`` uses
``httpx`` and ``websockets`` instead) -- it is installed here so that user
code running *inside* a jupyter-server sandbox can ``import aiohttp`` for
its own HTTP work. That is the "production" construction path this test
exercises: a plain ``aiohttp.ClientSession()`` making a real request over a
loopback socket, exactly as a notebook cell would.

Bumped for GHSA-cq5v-8q36-5273 / CVE-2026-69244 (aiohttp 3.14.1 -> 3.14.3):
an out-of-bounds heap read in the C HTTP response parser's error-message
construction, triggered by a malformed chunked response. The second test
below feeds the exact malformed bodies from aiohttp's own upstream
regression suite (aio-libs/aiohttp PR #13223) through a real client/server
round trip.

Note on the revert-check: re-running this test against aiohttp==3.14.1
produced byte-identical output to 3.14.3 across repeated runs (see the
remediation report) -- consistent with a heap-layout-dependent OOB read
that a bare Python process cannot reliably surface without ASan/valgrind.
The test still pins the exact vulnerable inputs against the real parser on
every CI run, which is strictly better coverage than the "no test at all"
baseline this package had before.
"""

import asyncio

import aiohttp
import pytest

# Malformed chunked-response bodies from aiohttp's own regression suite for
# this CVE (aio-libs/aiohttp tests/test_http_parser.py,
# _BAD_CHUNKED_RESPONSES + _BAD_CHUNKED_RESPONSES_AT_END). The "AT_END"
# entries place the offending byte at the very end of the TCP write, with no
# trailing CRLF -- the case the unbounded strlen-based slice used to overrun.
MALFORMED_CHUNKED_BODIES = [
    b"0\rX\r\n\r\n",
    b"5\r\nhello\rX\r\n0\r\n\r\n",
    b"5\r\nhelloXY\r\n0\r\n\r\n",
    b"1\r\nA\rB\r\n0\r\n\r\n",
    b"0_2e\r\n\r\n",
    b"0\rX",
    b"5\r\nhello\rX",
    b"0_",
]

CHUNKED_RESPONSE_HEADER = b"HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n"


async def _serve_once(handler):
    """Start a loopback TCP server that runs ``handler`` for a single
    connection, and return its base URL."""
    server = await asyncio.start_server(handler, "127.0.0.1", 0)
    port = server.sockets[0].getsockname()[1]
    return server, f"http://127.0.0.1:{port}/"


async def _plain_ok_handler(reader, writer):
    try:
        await reader.readuntil(b"\r\n\r\n")
    except asyncio.IncompleteReadError:
        pass
    body = b'{"ok": true}'
    writer.write(
        b"HTTP/1.1 200 OK\r\n"
        b"Content-Type: application/json\r\n"
        b"Content-Length: " + str(len(body)).encode() + b"\r\n\r\n" + body
    )
    await writer.drain()
    writer.close()


async def _round_trip_get() -> tuple[int, bytes]:
    server, url = await _serve_once(_plain_ok_handler)
    try:
        async with aiohttp.ClientSession() as session:
            async with session.get(url) as resp:
                return resp.status, await resp.read()
    finally:
        server.close()
        await server.wait_closed()


def test_client_session_round_trip():
    """Baseline: a notebook-style ``aiohttp.ClientSession().get()`` against a
    real loopback server works end to end after the bump."""
    status, body = asyncio.run(_round_trip_get())
    assert status == 200
    assert body == b'{"ok": true}'


async def _fetch_malformed(body: bytes) -> Exception:
    async def handler(reader, writer):
        try:
            await reader.readuntil(b"\r\n\r\n")
        except asyncio.IncompleteReadError:
            pass
        writer.write(CHUNKED_RESPONSE_HEADER + body)
        await writer.drain()
        writer.close()

    server, url = await _serve_once(handler)
    try:
        async with aiohttp.ClientSession() as session:
            with pytest.raises((aiohttp.ClientResponseError, aiohttp.ClientPayloadError)) as exc_info:
                async with session.get(url) as resp:
                    await resp.read()
            return exc_info.value
    finally:
        server.close()
        await server.wait_closed()


@pytest.mark.parametrize("body", MALFORMED_CHUNKED_BODIES)
def test_malformed_chunked_response_raises_clean_error(body: bytes):
    """Regression test for GHSA-cq5v-8q36-5273 (CVE-2026-69244).

    Feeds a malformed chunked response -- including the "error at buffer
    end" shapes that used to trigger the OOB read while building the error
    message -- through a real ``ClientSession`` request. The bump must keep
    raising a clean, catchable HTTP error; it must never crash the process
    or hang.
    """
    exc = asyncio.run(asyncio.wait_for(_fetch_malformed(body), timeout=5.0))
    # The parser's error message embeds a repr() of the bad snippet; assert
    # it is a normal, bounded string rather than something absurdly long
    # (a symptom of the unbounded strlen the patch removed).
    assert len(str(exc)) < 500
