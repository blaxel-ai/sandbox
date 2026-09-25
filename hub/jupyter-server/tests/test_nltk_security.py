"""Security smoke tests for the ``nltk`` package shipped in this image's Python
environment (see ``requirements.txt``).

``nltk`` is not imported by our own server code (``server/*.py``) -- like
``aiohttp`` (see ``test_aiohttp_smoke.py``) and ``pytest`` (see
``test_pytest_tmpdir_security.py``), it is installed here so that user code
running *inside* a jupyter-server sandbox can ``import nltk`` for its own NLP
work. It is also a hard runtime dependency of ``textblob`` (``nltk>=3.9``),
which is pinned in the same file, so it cannot simply be dropped. That user-
facing surface is the "production" path these tests exercise: the public
entry points a notebook cell reaches, not wrappers of our own.

Bumped 3.10.0 -> 3.10.3 for eleven Dependabot alerts. The four with a
distinguishable behavioural fix are covered below, one test each:

* GHSA-m4rf-3fr8-xwx3 / CVE-2026-79675 (**critical**) -- ``nltk.internals.java()``
  validated only the *global* JVM options set through ``config_java()``; its
  per-call ``options=`` parameter went straight to ``subprocess.Popen``
  unvalidated, so ``-agentpath:``/``-javaagent:``/``@argfile`` reached the JVM
  (arbitrary code execution). All four Stanford wrappers route through
  ``java()``, which is why it is the choke point tested here.
* GHSA-5gh2-94qg-qppq / CVE-2026-71513 (high) -- ``AllowlistUnpickler`` checked
  the pickle's *module* against its allowlist but never the *name*, and for
  pickle protocol >= 4 a dotted name is resolved by attribute traversal. With
  ``sklearn`` allowlisted (as ``TransitionParser.parse`` does), a pickle asking
  for ``sklearn.os.system`` reached ``os.system`` and executed.
* GHSA-6hwm-xvph-95vm / CVE-2026-78680 (high) -- two sites ran the Graphviz
  ``dot`` program by bare name, so a planted ``./dot`` could be executed in
  place of the real binary.
* GHSA-qx2g-xrx7-vfh8 / CVE-2026-72818 (high) -- ``TweetTokenizer``'s URL and
  email regexes backtracked catastrophically on long dotted input.

The remaining seven alerts (GHSA-cw6x-m8jw-qmrh, GHSA-f794-5jv7-7672,
GHSA-ff5c-cp5c-9wjf, GHSA-vp2x-qp44-57v7, GHSA-ww6m-cw3f-q94g,
GHSA-cv22-g7mw-8v73) are also closed by 3.10.3 and rest on the version floor
plus the resolved-environment check in the remediation report.

**Still unpatched at 3.10.3: GHSA-8mgp-746c-j5xp / CVE-2026-81726** (high) --
``TransitionParser.train``/``parse``, ``AveragedPerceptron.save``/``load``,
``PerceptronTagger.save_to_json`` and ``save_maxent_params`` use the builtin
``open()`` on caller-controlled model paths instead of the ``nltk.pathsec``
helpers, so they read and write outside the allowed roots. 3.10.3 is the
newest nltk on PyPI and no fixed release exists, so no bump can close it.
It only bites an application that turns on ``nltk.pathsec`` enforcement and
then lets untrusted input choose model paths; nothing in this image does
either. Deliberately left untested -- there is no fixed version to test
against. Revisit when nltk publishes one.
"""

import io
import multiprocessing
import os
import pickle
import shutil
import subprocess
import sys

import nltk
import pytest
from nltk.internals import java
from nltk.parse.transitionparser import _MODEL_ALLOWED_MODULES
from nltk.picklesec import allowlisted_pickle_load

# Added by the GHSA-5gh2 fix; absent on the vulnerable 3.10.0. Defaulting to ()
# rather than importing it directly keeps the revert-check meaningful: on a
# vulnerable version this test fails on its assertion (the security property
# regressed) instead of on an ImportError, which is far more informative.
_MODEL_ALLOWED_GLOBALS = getattr(
    sys.modules["nltk.parse.transitionparser"], "_MODEL_ALLOWED_GLOBALS", ()
)


def test_nltk_version_is_patched():
    """Belt-and-suspenders version assertion alongside the resolved-environment
    check in the remediation report.

    The ``isdigit`` guard is the boundary case: a naive ``int()`` over the
    parts would raise on a pre-release like ``3.11.0rc1``, and a comparator
    that stripped the suffix instead would read that pre-release as
    satisfying 3.10.3 without necessarily carrying the fixes. Fail explicitly
    on anything that is not three plain integers rather than guess.
    """
    parts = nltk.__version__.split(".")
    assert len(parts) >= 3 and all(
        p.isdigit() for p in parts[:3]
    ), f"unexpected nltk version {nltk.__version__!r}; re-derive this assertion"
    assert tuple(int(p) for p in parts[:3]) >= (3, 10, 3)


# --------------------------------------------------------------------------
# GHSA-m4rf-3fr8-xwx3 / CVE-2026-79675 (critical): JVM argument injection
# through java()'s per-call `options`.
# --------------------------------------------------------------------------

# Straight from the advisory's proof of concept. None contains whitespace, so
# a pass here means the flag allowlist rejected it -- not the separate
# shape guard that rejects whitespace and shell metacharacters in any flag.
DANGEROUS_JVM_OPTIONS = [
    "-agentpath:/tmp/evil.so",
    "-agentlib:jdwp=transport=dt_socket,server=y,address=*:5005",
    "-javaagent:/tmp/evil.jar",
    "@/tmp/argfile",
    "-XX:OnError=id",
]


@pytest.fixture
def stub_java_bin(monkeypatch, tmp_path):
    """Point nltk at a harmless stand-in for the JVM.

    No Java runtime is installed in this image, and ``java()`` resolves
    ``_java_bin`` (raising ``LookupError`` when it cannot) *before* it looks at
    the options. Stubbing the resolved binary is what lets these tests reach
    the option-handling code at all, and it keeps them hermetic: if validation
    were to regress, the "attack" runs ``/bin/echo`` and nothing else.
    """
    monkeypatch.setattr("nltk.internals._java_bin", "/bin/echo")
    # 3.10.3 also refuses a classpath entry that is relative, or absolute but
    # outside the nltk_data roots (``_verify_jar_sandbox``). That is a separate
    # guard raising ``UntrustedJarError`` -- not a ``ValueError`` -- so it could
    # not make the negative tests below pass by accident; registering a trusted
    # root anyway is what lets the positive control get far enough to spawn.
    classpath = tmp_path / "nltk_data"
    classpath.mkdir()
    monkeypatch.setattr("nltk.data.path", [str(classpath)])
    return str(classpath)


@pytest.mark.parametrize("option", DANGEROUS_JVM_OPTIONS)
def test_java_rejects_dangerous_per_call_options(option, stub_java_bin):
    """A dangerous JVM flag passed through ``java(options=...)`` -- the path the
    Stanford wrappers use for their ``java_options`` -- must be rejected before
    the process is spawned (GHSA-m4rf-3fr8-xwx3 / CVE-2026-79675).

    Matching on ``java_options`` anchors the failure to the option validator:
    a ``ValueError`` raised anywhere else in ``java()`` would not satisfy it.
    """
    with pytest.raises(ValueError, match="java_options"):
        java(
            ["Dummy"],
            classpath=stub_java_bin,
            options=[option],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )


def test_java_still_accepts_heap_flags(stub_java_bin):
    """Positive control for the test above.

    Without this, the parametrized test would pass just as well against a
    validator that rejected *every* option -- or against a ``java()`` broken in
    some unrelated way -- and would prove nothing about the fix. A legitimate
    heap flag, which is all NLTK's own wrappers ever pass, must still go
    through.
    """
    java(
        ["Dummy"],
        classpath=stub_java_bin,
        options=["-Xmx512m"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )


# --------------------------------------------------------------------------
# GHSA-5gh2-94qg-qppq / CVE-2026-71513 (high): AllowlistUnpickler resolved
# dotted names by attribute traversal, escaping the allowlisted namespace.
# --------------------------------------------------------------------------


def _dotted_name_pickle(module: str, name: str, argument: str) -> bytes:
    """Hand-assemble a protocol-4 pickle that calls ``<module>.<name>(argument)``.

    Built by hand rather than with ``pickle.dumps`` because ``pickle`` will not
    emit a dotted global: the whole point of the advisory is that the *loader*
    used to resolve one. Opcodes: PROTO 4, SHORT_BINUNICODE x2, STACK_GLOBAL,
    SHORT_BINUNICODE, TUPLE1, REDUCE, STOP.
    """

    def short_binunicode(text: str) -> bytes:
        encoded = text.encode()
        assert len(encoded) < 256
        return b"\x8c" + bytes([len(encoded)]) + encoded

    return (
        b"\x80\x04"
        + short_binunicode(module)
        + short_binunicode(name)
        + b"\x93"
        + short_binunicode(argument)
        + b"\x85R."
    )


def test_allowlisted_pickle_load_rejects_dotted_name(tmp_path):
    """A pickle reaching ``os.system`` by traversing an allowlisted namespace
    must be refused (GHSA-5gh2-94qg-qppq / CVE-2026-71513).

    ``sklearn`` is allowlisted because a fitted ``TransitionParser`` model
    genuinely needs it, and ``sklearn.os`` is a plain module attribute, so
    ``sklearn.os.system`` used to resolve and execute. The payload writes a
    marker file rather than doing anything destructive: if the guard ever
    regresses, the assertion below names the failure precisely instead of the
    test merely erroring somewhere else.
    """
    marker = tmp_path / "GADGET_EXECUTED"
    payload = _dotted_name_pickle("sklearn", "os.system", f"> '{marker}'")

    with pytest.raises(pickle.UnpicklingError, match="dotted name"):
        allowlisted_pickle_load(
            io.BytesIO(payload),
            allowed_modules=_MODEL_ALLOWED_MODULES,
            allowed_globals=_MODEL_ALLOWED_GLOBALS,
        )

    assert not marker.exists(), "the dotted-name gadget executed"


def test_allowlisted_pickle_load_still_loads_a_model_payload():
    """Positive control for the test above: the allowlist that blocks the gadget
    must still reconstruct what a real fitted model contains.

    A guard that rejected every global would satisfy the negative test while
    breaking ``TransitionParser.parse`` for every legitimate model, so assert
    the round trip through the same production allowlist. ``numpy.ndarray``
    reconstruction is the case that actually needs the broad ``numpy``
    namespace entry.
    """
    numpy = pytest.importorskip("numpy")
    original = numpy.arange(6, dtype=numpy.float64).reshape(2, 3)
    loaded = allowlisted_pickle_load(
        io.BytesIO(pickle.dumps(original, protocol=4)),
        allowed_modules=_MODEL_ALLOWED_MODULES,
        allowed_globals=_MODEL_ALLOWED_GLOBALS,
    )
    assert numpy.array_equal(loaded, original)


# --------------------------------------------------------------------------
# GHSA-6hwm-xvph-95vm / CVE-2026-78680 (high): the Graphviz `dot` program was
# executed by bare name, so PATH/CWD resolution could pick a planted binary.
# --------------------------------------------------------------------------

# Writes the marker with a shell redirection, a builtin, so the stub does not
# itself depend on PATH -- and reads no stdin, so a caller that pipes a dot
# source into it cannot deadlock.
_DOT_STUB = "#!/bin/sh\n> '%s'\nprintf '<svg/>'\nexit 0\n"


@pytest.mark.skipif(os.name != "posix", reason="plants an executable shell stub")
def test_dot_sites_refuse_a_planted_binary(tmp_path, monkeypatch):
    """Neither Graphviz call site may execute a ``./dot`` planted in the working
    directory (GHSA-6hwm-xvph-95vm / CVE-2026-78680).

    ``nltk.internals.find_binary`` refuses a CWD-relative match for a bare tool
    name and returns only a trusted absolute path; the fix runs that path at
    both sites instead of the bare name.

    ``AlignedSent._repr_svg_`` is the discriminating case here: on the
    vulnerable 3.10.0 it ran the bare name with no validation at all and
    executed the planted stub, while the patched version refuses (see the
    remediation report for the transcript). ``dot2img`` on 3.10.0 *did* call
    ``find_binary`` first -- it discarded the validated path afterwards -- so
    with ``.`` on PATH it already failed, just with a raw ``LookupError``
    instead of the documented message. Its pre-fix hole was Windows-specific
    (process creation implicitly searches the CWD there), which a POSIX runner
    cannot reproduce; it is asserted here for completeness, not as the signal.
    """
    planted = tmp_path / "dot"
    planted.write_text(_DOT_STUB % (tmp_path / "PLANTED_RAN"))
    planted.chmod(0o755)
    monkeypatch.chdir(tmp_path)
    # '.' first, so a bare-name exec resolves to the planted stub. Real system
    # directories are kept because find_binary shells out to `which`.
    monkeypatch.setenv("PATH", ".:/usr/bin:/bin")
    marker = tmp_path / "PLANTED_RAN"

    # Control: the stub really is reachable by bare name in this setup. Without
    # this, a broken PATH would make the assertions below pass for free.
    subprocess.run(["dot"], check=False, capture_output=True, stdin=subprocess.DEVNULL)
    assert marker.exists(), "test setup is wrong: the planted ./dot is not reachable"
    marker.unlink()

    from nltk.parse.dependencygraph import dot2img
    from nltk.translate.api import AlignedSent

    # The property under test is "the planted ./dot never runs". What happens instead depends
    # on the host: without a real Graphviz the lookup must fail with the documented message,
    # with one installed in /usr/bin or /bin nltk may run it (or fail on the input), and the
    # test must not care which. Devin Review flagged the original environment-dependent raise.
    real_dot = shutil.which("dot", path="/usr/bin:/bin")

    def assert_planted_stub_not_run(call, label):
        try:
            call()
        except Exception as exc:  # noqa: BLE001 - the error type is nltk's business
            if real_dot is None:
                assert "Cannot find the dot binary" in str(exc), f"{label}: unexpected error {exc!r}"
        else:
            assert real_dot is not None, f"{label}: no real dot on PATH, yet the lookup did not fail"
        assert not marker.exists(), f"{label} executed the planted ./dot"

    assert_planted_stub_not_run(lambda: dot2img("digraph G { a -> b }"), "dot2img")
    assert_planted_stub_not_run(lambda: AlignedSent(["a"], ["b"])._repr_svg_(), "AlignedSent._repr_svg_")
    assert not marker.exists(), "AlignedSent._repr_svg_ executed the planted ./dot"


# --------------------------------------------------------------------------
# GHSA-qx2g-xrx7-vfh8 / CVE-2026-72818 (high): catastrophic backtracking in
# TweetTokenizer's URL and email regexes.
# --------------------------------------------------------------------------

# DNS permits at most 127 labels in a name, and the fix bounds the regex's
# label repetition to `{0,126}` on exactly that reasoning. That bound is
# directly observable, which makes it a *deterministic* discriminator for this
# advisory -- no clock involved. See the test below.
MAX_MATCHABLE_LABELS = 126


def test_tweet_tokenizer_label_repetition_is_bounded():
    """The naked-domain label repetition must be bounded (GHSA-qx2g-xrx7-vfh8).

    This is the primary check for this advisory, deliberately in preference to
    a timing measurement: the fix replaced the unbounded ``(?:[.\\-][a-z0-9]+)*``
    with ``{0,126}``, and that bound changes tokenizer *output* in a way a
    clock cannot argue with. Verified against the version being replaced: on
    3.10.0 a 200-label string is still matched as one URL token, on 3.10.3 it
    is not.

    The first assertion is the half that keeps the fix honest -- a real domain
    name must still tokenize as a single URL, so the test would fail just as
    loudly if a future bound were tightened to something that breaks ordinary
    input.
    """
    from nltk.tokenize.casual import TweetTokenizer

    tokenizer = TweetTokenizer()

    within_limit = ("a." * MAX_MATCHABLE_LABELS) + "com"
    assert tokenizer.tokenize(within_limit) == [within_limit]

    beyond_limit = ("a." * (MAX_MATCHABLE_LABELS + 4)) + "com"
    assert tokenizer.tokenize(beyond_limit) != [beyond_limit], (
        "the naked-domain label repetition is unbounded again; the ReDoS fix "
        "for GHSA-qx2g-xrx7-vfh8 has regressed"
    )


# Upstream's own regression payloads, verbatim (nltk PR #3701).
REDOS_PAYLOADS = ("a." * 8000, "a.a-" * 8000, "http://a(" * 8000)

# Generous by design: a hang detector, not a performance measurement. Measured
# on this machine, all three payloads in one worker: 3.10.3 finishes in ~0.5 s
# total, while 3.10.0 needs 76.9 s for the first payload alone and minutes for
# the set (growth is super-quadratic: 8 KB -> 8.9 s, 16 KB -> 76.9 s). Two
# orders of magnitude of headroom, so shared-runner load cannot close the gap
# and a tighter threshold would only buy flakiness.
REDOS_TIMEOUT_SECONDS = 60


def _tokenize_redos_payloads():
    from nltk.tokenize.casual import TweetTokenizer

    tokenizer = TweetTokenizer()
    for payload in REDOS_PAYLOADS:
        tokenizer.tokenize(payload)


def test_tweet_tokenizer_does_not_backtrack_catastrophically():
    """``TweetTokenizer`` must tokenize long dotted input in bounded time
    (GHSA-qx2g-xrx7-vfh8 / CVE-2026-72818).

    ``TweetTokenizer`` is meant for untrusted social-media text, so before the
    fix a few kilobytes of input could pin a core for minutes. Run it in a
    spawned process with a hard timeout, the way upstream's own regression test
    does, so a regression fails fast instead of hanging this suite forever.

    This covers the *email* regex too, which the same commit bounded and which
    the label-bound test above does not reach. It is the secondary check for
    this advisory -- the deterministic bound assertion is the one that cannot
    be argued with on a loaded runner.
    """
    context = multiprocessing.get_context("spawn")
    process = context.Process(target=_tokenize_redos_payloads)
    process.start()
    process.join(REDOS_TIMEOUT_SECONDS)
    if process.is_alive():
        process.terminate()
        process.join()
        pytest.fail(
            f"TweetTokenizer did not finish within {REDOS_TIMEOUT_SECONDS}s on "
            "upstream's ReDoS payloads; the regex bounds have regressed"
        )
    assert process.exitcode == 0, f"tokenizer worker failed (exit {process.exitcode})"
