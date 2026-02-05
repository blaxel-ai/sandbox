import logging
import sys
import httpx
import asyncio
import time

from typing import Dict, Union, Literal, Set

from contextlib import asynccontextmanager
from fastapi import FastAPI, Request
from fastapi.responses import PlainTextResponse
from starlette.middleware.base import BaseHTTPMiddleware
from starlette.exceptions import HTTPException

from api.models.context import Context
from api.models.create_context import CreateContext
from api.models.execution_request import ExecutionRequest
from consts import JUPYTER_BASE_URL
from contexts import create_context, normalize_language
from messaging import ContextWebSocket
from stream import StreamingListJsonResponse
from utils.locks import LockedMap

logging.basicConfig(level=logging.DEBUG, stream=sys.stdout)
logger = logging.Logger(__name__)
http_logger = logging.getLogger("httpcore.http11")
http_logger.setLevel(logging.WARNING)


class RequestLoggingMiddleware(BaseHTTPMiddleware):
    async def dispatch(self, request: Request, call_next):
        start_time = time.time()
        response = await call_next(request)
        process_time = time.time() - start_time
        logger.info(f"{request.method} {request.url.path} {process_time:.3f}s")
        return response


websockets: Dict[Union[str, Literal["default"]], ContextWebSocket] = {}
default_websockets = LockedMap()
global client

MAX_TIMEOUT = 10.0  # Maximum timeout in seconds for retries
RETRY_DELAY = 0.5  # Delay between retries in seconds


async def wait_for_jupyter_ready_with_timeout(client: httpx.AsyncClient, timeout: float = MAX_TIMEOUT) -> bool:
    """Wait for Jupyter Server to be ready with a timeout."""
    start_time = time.time()
    attempt = 0

    while (time.time() - start_time) < timeout:
        try:
            response = await client.get(f"{JUPYTER_BASE_URL}/api/status", timeout=2.0)
            if response.status_code == 200:
                logger.info("Jupyter Server is ready")
                return True
        except Exception:
            pass

        attempt += 1
        if attempt % 4 == 0:  # Log every 2 seconds (4 attempts * 0.5s)
            elapsed = time.time() - start_time
            logger.info(f"Waiting for Jupyter Server to be ready ({elapsed:.1f}s/{timeout}s)...")

        await asyncio.sleep(RETRY_DELAY)

    logger.warning(f"Jupyter Server did not become ready within {timeout}s")
    return False


async def ensure_default_context(language: str = "python") -> ContextWebSocket:
    """Ensure default context exists, creating it with retry if needed. Returns singleton websocket."""
    global client

    # Check if default context already exists
    if language in default_websockets:
        context_id = default_websockets.get(language)
        if context_id and context_id in websockets:
            return websockets[context_id]

    # Wait for Jupyter to be ready with timeout
    if not await wait_for_jupyter_ready_with_timeout(client):
        raise Exception("Jupyter Server is not ready")

    # Create context with retry logic
    async with await default_websockets.get_lock(language):
        # Double-check after acquiring lock
        if language in default_websockets:
            context_id = default_websockets.get(language)
            if context_id and context_id in websockets:
                return websockets[context_id]

        # Create new context
        try:
            context = await create_context(client, websockets, language, "/app")
            default_websockets[language] = context.id
            websockets["default"] = websockets[context.id]
            logger.info(f"Created default {language} context: {context.id}")
            return websockets[context.id]
        except Exception as e:
            logger.error(f"Failed to create default {language} context: {e}")
            raise


@asynccontextmanager
async def lifespan(app: FastAPI):
    global client
    client = httpx.AsyncClient()

    logger.info("Starting Code Interpreter server...")

    yield

    # Will cleanup after application shuts down
    for ws in websockets.values():
        await ws.close()

    await client.aclose()


app = FastAPI(lifespan=lifespan)
app.add_middleware(RequestLoggingMiddleware)


@app.exception_handler(Exception)
async def global_exception_handler(request: Request, exc: Exception):
    """Catch all unhandled exceptions and return 400 instead of 500."""
    logger.error(f"Unhandled exception in {request.method} {request.url.path}: {exc}", exc_info=True)
    return PlainTextResponse(
        f"Error: {str(exc)}",
        status_code=400,
    )


@app.exception_handler(HTTPException)
async def http_exception_handler(request: Request, exc: HTTPException):
    """Handle HTTP exceptions - convert 500 to 400."""
    if exc.status_code == 500:
        logger.error(f"HTTP 500 in {request.method} {request.url.path}: {exc.detail}")
        return PlainTextResponse(
            exc.detail or "Error",
            status_code=400,
        )
    return PlainTextResponse(
        exc.detail,
        status_code=exc.status_code,
    )


logger.info("Starting Code Interpreter server")


@app.get("/health")
async def get_health():
    return "OK"


@app.post("/execute")
async def post_execute(request: Request, exec_request: ExecutionRequest):
    logger.info(f"Executing code: {exec_request.code}")

    if exec_request.context_id and exec_request.language:
        return PlainTextResponse(
            "Only one of context_id or language can be provided",
            status_code=400,
        )

    ws = None

    if exec_request.context_id:
        # Use specific context
        ws = websockets.get(exec_request.context_id, None)
        if not ws:
            return PlainTextResponse(
                f"Context {exec_request.context_id} not found",
                status_code=404,
            )
    elif exec_request.language:
        # Use language-specific default context
        language = normalize_language(exec_request.language)
        try:
            ws = await ensure_default_context(language)
        except Exception as e:
            logger.error(f"Failed to ensure default context for {language}: {e}")
            return PlainTextResponse(
                f"Failed to create context: {str(e)}",
                status_code=400,
            )
    else:
        # Use default python context
        try:
            ws = await ensure_default_context("python")
        except Exception as e:
            logger.error(f"Failed to ensure default python context: {e}")
            return PlainTextResponse(
                f"Failed to create default context: {str(e)}",
                status_code=400,
            )

    try:
        return StreamingListJsonResponse(
            ws.execute(
                exec_request.code,
                env_vars=exec_request.env_vars or {},
            )
        )
    except Exception as e:
        logger.error(f"Error executing code: {e}")
        # Execution errors are typically client errors (bad code, etc.)
        return PlainTextResponse(
            f"Error executing code: {str(e)}",
            status_code=400,
        )


@app.post("/contexts")
async def post_contexts(request: CreateContext) -> Context:
    logger.info("Creating a new context")

    language = normalize_language(request.language)
    cwd = request.cwd or "/app"

    # Ensure Jupyter is ready before creating context
    if not await wait_for_jupyter_ready_with_timeout(client):
        return PlainTextResponse(
            "Jupyter Server is not ready",
            status_code=503,
        )

    try:
        return await create_context(client, websockets, language, cwd)
    except Exception as e:
        logger.error(f"Failed to create context: {e}")
        # Context creation failures are usually client errors (invalid language, etc.)
        return PlainTextResponse(
            f"Failed to create context: {str(e)}",
            status_code=400,
        )


@app.get("/contexts")
async def get_contexts() -> Set[Context]:
    logger.info("Listing contexts")

    context_ids = websockets.keys()

    return set(
        Context(
            id=websockets[context_id].context_id,
            language=websockets[context_id].language,
            cwd=websockets[context_id].cwd,
        )
        for context_id in context_ids
    )


@app.post("/contexts/{context_id}/restart")
async def restart_context(context_id: str) -> None:
    logger.info(f"Restarting context {context_id}")

    ws = websockets.get(context_id, None)
    if not ws:
        return PlainTextResponse(
            f"Context {context_id} not found",
            status_code=404,
        )

    session_id = ws.session_id

    await ws.close()

    try:
        response = await client.post(
            f"{JUPYTER_BASE_URL}/api/kernels/{ws.context_id}/restart"
        )
        if not response.is_success:
            logger.error(f"Failed to restart context {context_id}: {response.status_code}")
            return PlainTextResponse(
                f"Failed to restart context {context_id}",
                status_code=400,
            )
    except Exception as e:
        logger.error(f"Error restarting context {context_id}: {e}")
        return PlainTextResponse(
            f"Failed to restart context {context_id}: {str(e)}",
            status_code=400,
        )

    ws = ContextWebSocket(
        ws.context_id,
        session_id,
        ws.language,
        ws.cwd,
    )

    await ws.connect()

    websockets[context_id] = ws


@app.delete("/contexts/{context_id}")
async def remove_context(context_id: str) -> None:
    logger.info(f"Removing context {context_id}")

    ws = websockets.get(context_id, None)
    if not ws:
        return PlainTextResponse(
            f"Context {context_id} not found",
            status_code=404,
        )

    try:
        await ws.close()
    except:  # noqa: E722
        pass

    try:
        response = await client.delete(f"{JUPYTER_BASE_URL}/api/kernels/{ws.context_id}")
        if not response.is_success:
            logger.error(f"Failed to remove context {context_id}: {response.status_code}")
            return PlainTextResponse(
                f"Failed to remove context {context_id}",
                status_code=400,
            )
    except Exception as e:
        logger.error(f"Error removing context {context_id}: {e}")
        return PlainTextResponse(
            f"Failed to remove context {context_id}: {str(e)}",
            status_code=400,
        )

    del websockets[context_id]

