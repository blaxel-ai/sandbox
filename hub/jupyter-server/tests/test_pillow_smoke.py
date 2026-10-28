"""Smoke tests for the ``pillow`` bump to 13.0.0 (GHSA-w889-685w-c8fh and the
other advisories fixed in 13.0.0).

Pillow is not imported by our server code; it ships in this image for user
code in notebook cells, directly or through matplotlib, imageio and
scikit-image. The tests go through those same entry points: an image saved
by matplotlib, re-read and converted by Pillow, and an imageio round trip.

The last two tests feed inputs from Pillow's own 13.0.0 regression suite
(``Tests/test_file_xbm.py::test_redos``, ``Tests/test_imagemorph.py::test_zero_width``)
in a subprocess, so a hang or a crash on a vulnerable version fails the test
instead of taking pytest down.
"""

import io
import subprocess
import sys

import imageio.v3 as iio
import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402
import numpy as np  # noqa: E402
from PIL import Image  # noqa: E402


def test_matplotlib_png_round_trip_through_pillow():
    fig, ax = plt.subplots(figsize=(2, 2), dpi=50)
    ax.plot([0, 1], [0, 1])
    buf = io.BytesIO()
    fig.savefig(buf, format="png")
    plt.close(fig)

    buf.seek(0)
    with Image.open(buf) as im:
        assert im.format == "PNG"
        assert im.size == (100, 100)
        out = io.BytesIO()
        im.convert("RGB").save(out, format="JPEG", quality=90)

    out.seek(0)
    with Image.open(out) as jpeg:
        assert jpeg.format == "JPEG"
        assert jpeg.size == (100, 100)


def test_imageio_round_trip_uses_pillow():
    pixels = np.arange(4 * 4 * 3, dtype=np.uint8).reshape(4, 4, 3)
    encoded = iio.imwrite("<bytes>", pixels, extension=".png", plugin="pillow")
    decoded = iio.imread(encoded, extension=".png", plugin="pillow")
    assert np.array_equal(decoded, pixels)


def _run(code):
    return subprocess.run([sys.executable, "-c", code], capture_output=True, text=True, timeout=60)


def test_xbm_header_regex_is_bounded():
    # GHSA-xgh8-jp6q-p5x8: XBM header parsing ReDoS. 13.0.0 rejects this
    # header immediately; the subprocess timeout is a hang detector.
    result = _run(
        "from io import BytesIO\n"
        "from PIL import XbmImagePlugin\n"
        "redos = b''\n"
        "for prop in (b'width', b'height', b'x_hot', b'y_hot'):\n"
        "    redos += b'#define' + b' ' * 60 + b'_' + prop + b' 1\\r'\n"
        "redos += b'\\r' * 60 + b'_bits' + b'A' * 512\n"
        "try:\n"
        "    XbmImagePlugin.XbmImageFile(BytesIO(redos))\n"
        "except SyntaxError:\n"
        "    print('rejected')\n"
    )
    assert result.returncode == 0, result.stderr
    assert result.stdout.strip() == "rejected"


def test_imagemorph_zero_width_image():
    # GHSA-58r4-6rv3-gfh8: MorphOp.apply() out-of-bounds write on zero-width images.
    result = _run(
        "from PIL import Image, ImageMorph\n"
        "im = Image.new('L', (0, 1))\n"
        "count, out = ImageMorph.MorphOp(op_name='erosion8').apply(im)\n"
        "assert out.size == (0, 1), out.size\n"
        "print('ok', count)\n"
    )
    assert result.returncode == 0, result.stderr
    assert result.stdout.startswith("ok")
