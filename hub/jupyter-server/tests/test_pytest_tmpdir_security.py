"""Smoke test for the ``pytest`` package shipped in this image's Python
environment (see ``requirements.txt``).

``pytest`` is not imported by our own server code (``server/*.py``) -- like
``aiohttp`` (see ``test_aiohttp_smoke.py``), it is installed here so that
user code running *inside* a jupyter-server sandbox can write and run its
own tests. That is the "production" surface this test exercises: pytest's
own ``basetemp`` resolution, exactly as it runs for any test session in this
image.

Bumped for GHSA-6w46-j5rx-g56g / CVE-2025-71176 (pytest 8.3.5 -> 9.0.3):
pytest's base temp directory used the predictable name
``/tmp/pytest-of-{user}``. If that path was ever replaced by a symlink
(TOCTOU / symlink-swapping: a local attacker races to recreate the
directory as a symlink to somewhere they control after it's removed, or
plants it before pytest is ever run under that user), pytest would silently
follow the symlink and use whatever directory it points at, letting a local
attacker redirect another user's test artifacts. Fixed by
pytest-dev/pytest#14343: ``TempPathFactory.getbasetemp()`` now stats
``pytest-of-{user}`` with ``follow_symlinks=False`` and raises ``OSError``
if it is a symlink at all.

This test reproduces pytest's own upstream regression test for the fix
(testing/test_tmpdir.py::test_tmp_path_factory_doesnt_follow_symlinks) via
the internal ``_pytest.tmpdir.TempPathFactory`` API and the
``PYTEST_DEBUG_TEMPROOT`` escape hatch, which is the only way to point
pytest's temproot resolution at a throwaway directory instead of the real
``/tmp``. This is a deliberate coupling to an internal, undocumented pytest
API rather than a public one -- there is no public entry point for this
behavior -- so a future pytest release could rename or remove it with
nothing wrong in the dependency. That specific failure mode (the internal
API moving or its constructor signature changing) is guarded below to
``skip`` with an explanatory message instead of failing the CI gate on an
unrelated future dependency PR; an assertion failure inside the test body
(the ``OSError``/message-match not raising) is left as a hard failure,
since that would mean the actual security property regressed.

Revert-check: run against pytest==8.3.5 (the version this replaces), the
same call does not raise -- it silently accepts the symlink and returns a
path underneath the attacker-controlled directory. See the remediation
report for the full transcript.
"""

import os
import shutil
from pathlib import Path

import pytest

# _pytest.tmpdir.TempPathFactory is an internal, undocumented API (see module
# docstring) -- a future pytest release can rename/remove it or reorder its
# positional constructor arguments with nothing wrong in the dependency being
# bumped. Import it defensively so that happening turns this test into an
# informative skip instead of a hard failure that would block an unrelated
# future dependency PR's CI gate (flagged by devin-ai-integration on this PR).
try:
    from _pytest.tmpdir import TempPathFactory

    _import_error: Exception | None = None
except ImportError as exc:  # pragma: no cover - depends on pytest internals
    TempPathFactory = None  # type: ignore[assignment,misc]
    _import_error = exc


def _new_temp_path_factory() -> "TempPathFactory":
    """Construct a TempPathFactory, skipping (not failing) the test if
    pytest's internal constructor shape has changed -- see module docstring
    and the guard above."""
    if TempPathFactory is None:
        pytest.skip(
            "pytest moved or removed _pytest.tmpdir.TempPathFactory "
            f"({_import_error}); this test targets an internal pytest API "
            "and needs re-deriving against the new pytest internals -- "
            "see this file's module docstring"
        )
    try:
        return TempPathFactory(None, 3, "all", lambda *args: None, _ispytest=True)
    except TypeError as exc:
        pytest.skip(
            f"_pytest.tmpdir.TempPathFactory's constructor signature changed "
            f"({exc}); this test targets an internal pytest API and needs "
            "re-deriving against the new pytest internals -- see this "
            "file's module docstring"
        )


@pytest.mark.skipif(
    not hasattr(os, "getuid") or os.stat not in os.supports_follow_symlinks,
    reason="checks unix permissions and symlinks",
)
def test_tmp_path_factory_rejects_symlinked_basetemp(tmp_path: Path, monkeypatch) -> None:
    """A pytest-of-{user} base directory that is a symlink must be rejected,
    not silently followed (GHSA-6w46-j5rx-g56g / CVE-2025-71176)."""
    attacker_controlled = tmp_path / "attacker_controlled"
    attacker_controlled.mkdir()

    # Point pytest's temproot resolution at this test's own tmp_path instead
    # of the real /tmp, so the test is hermetic (no writes outside tmp_path,
    # no interaction with any real /tmp/pytest-of-* directory on the host).
    monkeypatch.setenv("PYTEST_DEBUG_TEMPROOT", str(tmp_path))

    # First resolution creates the real pytest-of-{user} directory; capture
    # its path, then remove it and replace it with a symlink to a directory
    # an "attacker" controls.
    tmp_factory = _new_temp_path_factory()
    pytest_of_user = tmp_factory.getbasetemp().parent
    assert "pytest-of-" in str(pytest_of_user)
    shutil.rmtree(pytest_of_user)
    pytest_of_user.symlink_to(attacker_controlled)

    # A fresh factory must now refuse to use it.
    tmp_factory = _new_temp_path_factory()
    with pytest.raises(OSError, match=r"temporary directory .* is a symbolic link"):
        tmp_factory.getbasetemp()


def test_pytest_version_is_patched() -> None:
    """Belt-and-suspenders version assertion alongside the lockfile check."""
    assert tuple(int(p) for p in pytest.__version__.split(".")[:2]) >= (9, 0)
