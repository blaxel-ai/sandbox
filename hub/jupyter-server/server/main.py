import logging
import sys
import httpx
import asyncio

from typing import Dict, Union, Literal, Set

from contextlib import asynccontextmanager
from fastapi import FastAPI, Request
from fastapi.responses import PlainTextResponse

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


websockets: Dict[Union[str, Literal["default"]], ContextWebSocket] = {}
default_websockets = LockedMap()
global client
jupyter_init_task: asyncio.Task = None

MAX_RETRIES = 60  # Maximum number of retry attempts
RETRY_DELAY = 1.0  # Delay between retries in seconds


async def wait_for_jupyter_ready(client: httpx.AsyncClient) -> bool:
    """Wait for Jupyter Server to be ready by checking the /api/status endpoint."""
    for attempt in range(MAX_RETRIES):
        try:
            response = await client.get(f"{JUPYTER_BASE_URL}/api/status", timeout=2.0)
            if response.status_code == 200:
                logger.info("Jupyter Server is ready")
                return True
        except Exception as e:
            if attempt % 10 == 0:  # Log every 10 attempts
                logger.info(f"Waiting for Jupyter Server to be ready (attempt {attempt + 1}/{MAX_RETRIES})...")
            await asyncio.sleep(RETRY_DELAY)

    return False


async def initialize_jupyter_contexts():
    """Initialize Jupyter contexts in the background after server starts."""
    global client
    logger.info("Starting background task to initialize Jupyter contexts...")

    if not await wait_for_jupyter_ready(client):
        logger.error(f"Jupyter Server did not become ready after {MAX_RETRIES} attempts")
        logger.info("Server will retry on first request")
        return

    try:
        python_context = await create_context(
            client, websockets, "python", "/app"
        )
        default_websockets["python"] = python_context.id
        websockets["default"] = websockets[python_context.id]

        logger.info("Connected to default runtime")
    except Exception as e:
        logger.warning(f"Failed to initialize default context: {e}. Will retry on first request.")


@asynccontextmanager
async def lifespan(app: FastAPI):
    global client, jupyter_init_task
    client = httpx.AsyncClient()

    # Start the server immediately by yielding
    # Then initialize Jupyter contexts in a background task
    logger.info("Starting Code Interpreter server...")
    jupyter_init_task = asyncio.create_task(initialize_jupyter_contexts())

    yield

    # Cleanup: cancel the background task if it's still running
    if jupyter_init_task and not jupyter_init_task.done():
        logger.info("Cancelling Jupyter initialization task...")
        jupyter_init_task.cancel()
        try:
            await jupyter_init_task
        except asyncio.CancelledError:
            pass

    # Will cleanup after application shuts down
    for ws in websockets.values():
        await ws.close()

    await client.aclose()


app = FastAPI(lifespan=lifespan)

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

    context_id = None
    if exec_request.language:
        language = normalize_language(exec_request.language)

        async with await default_websockets.get_lock(language):
            context_id = default_websockets.get(language)

            if not context_id:
                try:
                    context = await create_context(
                        client, websockets, language, "/app"
                    )
                except Exception as e:
                    return PlainTextResponse(str(e), status_code=500)

                context_id = context.id
                default_websockets[language] = context_id

    elif exec_request.context_id:
        context_id = exec_request.context_id

    if context_id:
        ws = websockets.get(context_id, None)
    else:
        ws = websockets["default"]

    if not ws:
        # Try to initialize default context if it doesn't exist
        if not websockets.get("default"):
            logger.info("Default context not found, attempting to create it...")
            if not await wait_for_jupyter_ready(client):
                return PlainTextResponse(
                    "Jupyter Server is not ready. Please try again later.",
                    status_code=400,
                )
            try:
                python_context = await create_context(
                    client, websockets, "python", "/app"
                )
                default_websockets["python"] = python_context.id
                websockets["default"] = websockets[python_context.id]
                ws = websockets["default"]
            except Exception as e:
                return PlainTextResponse(
                    f"Failed to create default context: {str(e)}",
                    status_code=503,
                )

        if not ws:
            return PlainTextResponse(
                f"Context {exec_request.context_id} not found",
                status_code=404,
            )

    return StreamingListJsonResponse(
        ws.execute(
            exec_request.code,
            env_vars=exec_request.env_vars or {},
        )
    )


@app.post("/contexts")
async def post_contexts(request: CreateContext) -> Context:
    logger.info("Creating a new context")

    language = normalize_language(request.language)
    cwd = request.cwd or "/app"

    try:
        return await create_context(client, websockets, language, cwd)
    except Exception as e:
        return PlainTextResponse(str(e), status_code=500)


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

    response = await client.post(
        f"{JUPYTER_BASE_URL}/api/kernels/{ws.context_id}/restart"
    )
    if not response.is_success:
        return PlainTextResponse(
            f"Failed to restart context {context_id}",
            status_code=500,
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

    response = await client.delete(f"{JUPYTER_BASE_URL}/api/kernels/{ws.context_id}")
    if not response.is_success:
        return PlainTextResponse(
            f"Failed to remove context {context_id}",
            status_code=500,
        )

    del websockets[context_id]

