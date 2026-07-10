"""
sigo_client - Modern Python client for sigoREST

A production-ready, OpenAI-compatible client with sync and async support.
"""

from .client import (
    SigoClient,
    AsyncSigoClient,
    SigoError,
    SigoAPIError,
    SigoConnectionError,
    SigoTimeoutError,
)

from .models import (
    ChatCompletion,
    ChatCompletionMessage,
    ChatCompletionChunk,
    Model,
    ModelList,
    HealthResponse,
    MemoryBlock,
    Usage,
)

__version__ = "2.0.0"
__all__ = [
    "SigoClient",
    "AsyncSigoClient",
    "SigoError",
    "SigoAPIError",
    "SigoConnectionError",
    "SigoTimeoutError",
    "ChatCompletion",
    "ChatCompletionMessage",
    "ChatCompletionChunk",
    "Model",
    "ModelList",
    "HealthResponse",
    "MemoryBlock",
    "Usage",
]
