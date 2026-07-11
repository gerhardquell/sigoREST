import json
import time
from typing import Any, Dict, Iterator, List, Optional, Union, AsyncIterator

import httpx
from pydantic import ValidationError
from .models import (
    ChatCompletion,
    ChatCompletionChunk,
    Model,
    ModelList,
    HealthResponse,
    MemoryBlock,
    ChatCompletionMessage,
    ChatCompletionChoice,
    Usage,
)


class SigoError(Exception):
    """Base exception for sigoREST client errors."""
    pass


class SigoAPIError(SigoError):
    """API returned an error response."""
    def __init__(self, message: str, status_code: Optional[int] = None, response: Optional[Dict] = None):
        super().__init__(message)
        self.status_code = status_code
        self.response = response or {}


class SigoConnectionError(SigoError):
    """Cannot connect to the sigoREST server."""
    pass


class SigoTimeoutError(SigoError):
    """Request timed out."""
    pass


class BaseSigoClient:
    """Base class with shared functionality."""

    def __init__(self, base_url: str = "http://127.0.0.1:9080", timeout: int = 180):
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self._headers = {"Content-Type": "application/json", "User-Agent": "sigo-client/2.0.0"}

    def _build_url(self, path: str) -> str:
        return f"{self.base_url}{path}"


class SigoClient(BaseSigoClient):
    """
    Synchronous client for sigoREST with OpenAI-compatible interface.

    Example:
        >>> client = SigoClient()
        >>> response = client.chat.completions.create(
        ...     model="cl5-s",
        ...     messages=[{"role": "user", "content": "Hallo!"}]
        ... )
        >>> print(response.content)
    """

    def __init__(self, base_url: str = "http://127.0.0.1:9080", timeout: int = 180):
        super().__init__(base_url, timeout)
        self.client = httpx.Client(timeout=timeout, headers=self._headers)
        self.chat = self._Chat(self)

    def __enter__(self):
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        self.close()

    def close(self):
        """Close the HTTP client."""
        self.client.close()

    def ping(self) -> bool:
        """Check if server is alive."""
        try:
            resp = self.client.get(self._build_url("/ping"), timeout=5.0)
            return resp.status_code == 200 and resp.text.strip() == "pong"
        except Exception:
            return False

    def health(self) -> HealthResponse:
        """Get detailed server health."""
        resp = self._request("GET", "/api/health")
        return HealthResponse.model_validate(resp)

    def models(self) -> List[Model]:
        """List all available models (OpenAI compatible)."""
        resp = self._request("GET", "/v1/models")
        model_list = ModelList.model_validate(resp)
        return model_list.data

    def list_models(self) -> List[Model]:
        """Alias for models() - returns rich model info."""
        return self.models()

    def get_memory(self) -> MemoryBlock:
        """Get the global memory block."""
        data = self._request("GET", "/api/memory")
        return MemoryBlock.model_validate(data)

    def set_memory(self, content: str, cache: bool = True) -> MemoryBlock:
        """Set the global memory/system prompt."""
        payload = {"content": content, "cache": cache}
        data = self._request("PUT", "/api/memory", json=payload)
        return MemoryBlock.model_validate(data)

    def _request(self, method: str, path: str, **kwargs) -> Dict[str, Any]:
        """Internal request helper with error handling."""
        url = self._build_url(path)
        try:
            response = self.client.request(method, url, **kwargs)
            response.raise_for_status()
            if not response.content:
                return {}
            return response.json()
        except httpx.HTTPStatusError as e:
            try:
                error_data = e.response.json()
                message = error_data.get("error", {}).get("message", str(e))
            except Exception:
                message = str(e)
            raise SigoAPIError(
                message=message,
                status_code=e.response.status_code,
                response=error_data if 'error_data' in locals() else None,
            ) from e
        except httpx.ConnectError as e:
            raise SigoConnectionError(f"Cannot connect to sigoREST at {self.base_url}: {e}") from e
        except httpx.TimeoutException as e:
            raise SigoTimeoutError(f"Request to {url} timed out") from e
        except Exception as e:
            raise SigoError(f"Request failed: {e}") from e

    class _Chat:
        """OpenAI-compatible chat interface."""

        def __init__(self, client: "SigoClient"):
            self.client = client

        @property
        def completions(self):
            return self._Completions(self.client)

        class _Completions:
            def __init__(self, client: "SigoClient"):
                self.client = client

            def create(
                self,
                model: str,
                messages: List[Dict[str, str]],
                temperature: Optional[float] = None,
                max_tokens: Optional[int] = None,
                session_id: Optional[str] = None,
                timeout: Optional[int] = None,
                retries: int = 3,
                stream: bool = False,
            ) -> Union[ChatCompletion, Iterator["ChatCompletionChunk"]]:
                """Create a chat completion (OpenAI compatible).

                If stream=True, returns an iterator of ChatCompletionChunk objects
                using real Server-Sent Events from the server.
                """
                if stream:
                    return self._create_stream(model, messages, temperature, max_tokens, session_id, timeout, retries)

                payload = {
                    "model": model,
                    "messages": messages,
                    "retries": retries,
                }
                if temperature is not None:
                    payload["temperature"] = temperature
                if max_tokens is not None:
                    payload["max_tokens"] = max_tokens
                if session_id:
                    payload["session_id"] = session_id
                if timeout:
                    payload["timeout"] = timeout

                data = self.client._request(
                    "POST",
                    "/v1/chat/completions",
                    json=payload,
                    timeout=timeout or self.client.timeout,
                )

                # Convert to our Pydantic model
                try:
                    completion = ChatCompletion.model_validate(data)
                    completion.raw_response = data
                    return completion
                except ValidationError:
                    # Fallback for non-standard response format
                    if "choices" in data and data["choices"]:
                        content = data["choices"][0].get("message", {}).get("content", "")
                        return ChatCompletion(
                            id=data.get("id", "chatcmpl-sigo"),
                            created=data.get("created", int(time.time())),
                            model=data.get("model", model),
                            choices=[
                                ChatCompletionChoice(
                                    index=0,
                                    message=ChatCompletionMessage(role="assistant", content=content),
                                    finish_reason=data.get("finish_reason"),
                                )
                            ],
                            usage=Usage.model_validate(data.get("usage", {})) if data.get("usage") else None,
                            raw_response=data,
                        )
                    raise

            def _create_stream(self, model, messages, temperature=None, max_tokens=None,
                             session_id=None, timeout=None, retries=3) -> Iterator["ChatCompletionChunk"]:
                """Real SSE streaming using httpx. Requires server-side streaming support."""
                payload = {
                    "model": model,
                    "messages": messages,
                    "stream": True,
                    "retries": retries,
                }
                if temperature is not None:
                    payload["temperature"] = temperature
                if max_tokens is not None:
                    payload["max_tokens"] = max_tokens
                if session_id:
                    payload["session_id"] = session_id
                if timeout:
                    payload["timeout"] = timeout

                url = self.client._build_url("/v1/chat/completions")
                with self.client.client.stream(
                    "POST",
                    url,
                    json=payload,
                    headers={"Accept": "text/event-stream"},
                    timeout=timeout or self.client.timeout,
                ) as response:
                    response.raise_for_status()

                    for line in response.iter_lines():
                        line = line.strip()
                        if not line or line.startswith(":"):
                            continue
                        if line.startswith("data: "):
                            data_str = line[6:].strip()
                            if data_str == "[DONE]":
                                break
                            try:
                                chunk_data = json.loads(data_str)
                                yield ChatCompletionChunk(
                                    id=chunk_data.get("id", f"chatcmpl-{int(time.time())}"),
                                    created=chunk_data.get("created", int(time.time())),
                                    model=chunk_data.get("model", model),
                                    choices=chunk_data.get("choices", []),
                                )
                            except json.JSONDecodeError:
                                continue


# Async variant
class AsyncSigoClient(BaseSigoClient):
    """Asynchronous client for sigoREST."""

    def __init__(self, base_url: str = "http://127.0.0.1:9080", timeout: int = 180):
        super().__init__(base_url, timeout)
        self.client = httpx.AsyncClient(timeout=timeout, headers=self._headers)
        self.chat = self._Chat(self)

    async def __aenter__(self):
        return self

    async def __aexit__(self, exc_type, exc_val, exc_tb):
        await self.close()

    async def close(self):
        await self.client.aclose()

    async def ping(self) -> bool:
        try:
            resp = await self.client.get(self._build_url("/ping"), timeout=5.0)
            return resp.status_code == 200 and resp.text.strip() == "pong"
        except Exception:
            return False

    async def health(self) -> HealthResponse:
        resp = await self._request("GET", "/api/health")
        return HealthResponse.model_validate(resp)

    async def models(self) -> List[Model]:
        resp = await self._request("GET", "/v1/models")
        model_list = ModelList.model_validate(resp)
        return model_list.data

    async def list_models(self) -> List[Model]:
        return await self.models()

    async def get_memory(self) -> MemoryBlock:
        data = await self._request("GET", "/api/memory")
        return MemoryBlock.model_validate(data)

    async def set_memory(self, content: str, cache: bool = True) -> MemoryBlock:
        payload = {"content": content, "cache": cache}
        data = await self._request("PUT", "/api/memory", json=payload)
        return MemoryBlock.model_validate(data)

    async def _request(self, method: str, path: str, **kwargs) -> Dict[str, Any]:
        url = self._build_url(path)
        try:
            response = await self.client.request(method, url, **kwargs)
            response.raise_for_status()
            if not response.content:
                return {}
            return response.json()
        except httpx.HTTPStatusError as e:
            try:
                error_data = e.response.json()
                message = error_data.get("error", {}).get("message", str(e))
            except Exception:
                message = str(e)
            raise SigoAPIError(
                message=message,
                status_code=e.response.status_code,
                response=error_data if 'error_data' in locals() else None,
            ) from e
        except httpx.ConnectError as e:
            raise SigoConnectionError(f"Cannot connect to sigoREST at {self.base_url}: {e}") from e
        except httpx.TimeoutException as e:
            raise SigoTimeoutError(f"Request to {url} timed out") from e
        except Exception as e:
            raise SigoError(f"Request failed: {e}") from e

    class _Chat:
        def __init__(self, client: "AsyncSigoClient"):
            self.client = client

        @property
        def completions(self):
            return self._Completions(self.client)

        class _Completions:
            def __init__(self, client: "AsyncSigoClient"):
                self.client = client

            async def create(
                self,
                model: str,
                messages: List[Dict[str, str]],
                temperature: Optional[float] = None,
                max_tokens: Optional[int] = None,
                session_id: Optional[str] = None,
                timeout: Optional[int] = None,
                retries: int = 3,
                stream: bool = False,
            ) -> Union[ChatCompletion, AsyncIterator["ChatCompletionChunk"]]:
                if stream:
                    return self._create_stream(model, messages, temperature, max_tokens, session_id, timeout, retries)

                payload = {
                    "model": model,
                    "messages": messages,
                    "retries": retries,
                }
                if temperature is not None: payload["temperature"] = temperature
                if max_tokens is not None: payload["max_tokens"] = max_tokens
                if session_id: payload["session_id"] = session_id
                if timeout: payload["timeout"] = timeout

                data = await self.client._request(
                    "POST", "/v1/chat/completions", json=payload, timeout=timeout or self.client.timeout
                )

                try:
                    completion = ChatCompletion.model_validate(data)
                    completion.raw_response = data
                    return completion
                except ValidationError:
                    if "choices" in data and data["choices"]:
                        content = data["choices"][0].get("message", {}).get("content", "")
                        return ChatCompletion(
                            id=data.get("id", "chatcmpl-sigo"),
                            created=data.get("created", int(time.time())),
                            model=data.get("model", model),
                            choices=[ChatCompletionChoice(
                                index=0,
                                message=ChatCompletionMessage(role="assistant", content=content),
                                finish_reason=data.get("finish_reason"),
                            )],
                            usage=Usage.model_validate(data.get("usage", {})) if data.get("usage") else None,
                            raw_response=data,
                        )
                    raise

            async def _create_stream(self, model, messages, temperature=None, max_tokens=None,
                             session_id=None, timeout=None, retries=3) -> AsyncIterator["ChatCompletionChunk"]:
                """Real async SSE streaming using httpx.AsyncClient."""
                payload = {
                    "model": model,
                    "messages": messages,
                    "stream": True,
                    "retries": retries,
                }
                if temperature is not None:
                    payload["temperature"] = temperature
                if max_tokens is not None:
                    payload["max_tokens"] = max_tokens
                if session_id:
                    payload["session_id"] = session_id
                if timeout:
                    payload["timeout"] = timeout

                url = self.client._build_url("/v1/chat/completions")
                async with self.client.client.stream(
                    "POST",
                    url,
                    json=payload,
                    headers={"Accept": "text/event-stream"},
                    timeout=timeout or self.client.timeout,
                ) as response:
                    response.raise_for_status()

                    async for line in response.aiter_lines():
                        line = line.strip()
                        if not line or line.startswith(":"):
                            continue
                        if line.startswith("data: "):
                            data_str = line[6:].strip()
                            if data_str == "[DONE]":
                                break
                            try:
                                chunk_data = json.loads(data_str)
                                choices = chunk_data.get("choices", [])
                                if not isinstance(choices, list):
                                    choices = []
                                yield ChatCompletionChunk(
                                    id=chunk_data.get("id", f"chatcmpl-async-{int(time.time())}"),
                                    created=chunk_data.get("created", int(time.time())),
                                    model=chunk_data.get("model", model),
                                    choices=choices,
                                )
                            except json.JSONDecodeError:
                                continue
