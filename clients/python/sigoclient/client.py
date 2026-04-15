"""
SigoClient - Main client implementation for sigoREST API
"""

import json
import requests
from typing import Optional, List, Dict, Any, Iterator
from dataclasses import dataclass


class SigoError(Exception):
    """Base exception for sigoREST client errors"""
    pass


class SigoAPIError(SigoError):
    """API error with response details"""

    def __init__(self, message: str, status_code: int = None, response: Dict = None):
        super().__init__(message)
        self.status_code = status_code
        self.response = response


@dataclass
class ChatMessage:
    """A chat message"""
    role: str
    content: str


@dataclass
class ChatResponse:
    """Response from a chat completion"""
    content: str
    model: str
    session_id: Optional[str] = None
    raw_response: Optional[Dict] = None


@dataclass
class ModelInfo:
    """Information about an available model"""
    id: str
    shortcode: str
    input_cost: float
    output_cost: float
    max_input_tokens: int
    max_output_tokens: int


class SigoClient:
    """
    Client for sigoREST API

    Example:
        >>> client = SigoClient("http://localhost:9080")
        >>> response = client.chat("claude-h", "Hello!")
        >>> print(response.content)
    """

    def __init__(self, base_url: str = "http://127.0.0.1:9080", timeout: int = 180):
        """
        Initialize the client

        Args:
            base_url: URL of the sigoREST server
            timeout: Default timeout for requests in seconds
        """
        self.base_url = base_url.rstrip("/")
        self.timeout = timeout
        self._session = requests.Session()

    def _request(self, method: str, path: str, **kwargs) -> Dict:
        """Make a request to the API"""
        url = f"{self.base_url}{path}"

        try:
            response = self._session.request(
                method=method,
                url=url,
                timeout=kwargs.pop("timeout", self.timeout),
                **kwargs
            )
            response.raise_for_status()
            return response.json() if response.content else {}
        except requests.exceptions.HTTPError as e:
            try:
                error_data = e.response.json()
                message = error_data.get("error", {}).get("message", str(e))
            except:
                message = str(e)
            raise SigoAPIError(
                message=message,
                status_code=e.response.status_code,
                response=None
            )
        except requests.exceptions.ConnectionError:
            raise SigoError(f"Cannot connect to sigoREST at {self.base_url}")
        except requests.exceptions.Timeout:
            raise SigoError(f"Request to {url} timed out")
        except Exception as e:
            raise SigoError(f"Request failed: {e}")

    def ping(self) -> bool:
        """
        Check if server is alive

        Returns:
            True if server responds with "pong"
        """
        try:
            response = self._session.get(
                f"{self.base_url}/ping",
                timeout=5
            )
            return response.status_code == 200 and response.text == "pong"
        except:
            return False

    def health(self) -> Dict[str, Any]:
        """
        Get server health status

        Returns:
            Dict with status, timestamp, available_models, etc.
        """
        return self._request("GET", "/api/health")

    def list_models(self) -> List[ModelInfo]:
        """
        List all available models

        Returns:
            List of ModelInfo objects
        """
        data = self._request("GET", "/api/models")
        return [
            ModelInfo(
                id=m.get("id", ""),
                shortcode=m.get("shortcode", ""),
                input_cost=m.get("input_cost", 0.0),
                output_cost=m.get("output_cost", 0.0),
                max_input_tokens=m.get("max_input_tokens", 0),
                max_output_tokens=m.get("max_output_tokens", 0)
            )
            for m in data
        ]

    def chat(
        self,
        model: str,
        message: str,
        session_id: Optional[str] = None,
        system_prompt: Optional[str] = None,
        temperature: Optional[float] = None,
        max_tokens: Optional[int] = None,
        timeout: Optional[int] = None,
        retries: int = 3
    ) -> ChatResponse:
        """
        Send a chat completion request

        Args:
            model: Model shortcode (e.g., "claude-h", "gpt41") or full ID
            message: The user message
            session_id: Optional session ID for conversation continuity
            system_prompt: Optional system prompt
            temperature: Optional temperature (0.0-2.0)
            max_tokens: Optional max tokens to generate
            timeout: Optional request timeout override
            retries: Number of retries (default: 3)

        Returns:
            ChatResponse with the assistant's reply

        Raises:
            SigoAPIError: If the API returns an error
            SigoError: If the request fails
        """
        messages = []
        if system_prompt:
            messages.append({"role": "system", "content": system_prompt})
        messages.append({"role": "user", "content": message})

        payload = {
            "model": model,
            "messages": messages,
            "retries": retries
        }

        if temperature is not None:
            payload["temperature"] = temperature
        if max_tokens is not None:
            payload["max_tokens"] = max_tokens
        if session_id is not None:
            payload["session_id"] = session_id
        if timeout is not None:
            payload["timeout"] = timeout

        data = self._request(
            "POST",
            "/v1/chat/completions",
            headers={"Content-Type": "application/json"},
            json=payload,
            timeout=timeout or self.timeout
        )

        choices = data.get("choices", [])
        if not choices:
            raise SigoError("No choices in response")

        content = choices[0].get("message", {}).get("content", "")

        return ChatResponse(
            content=content,
            model=data.get("model", model),
            session_id=session_id,
            raw_response=data
        )

    def chat_stream(
        self,
        model: str,
        message: str,
        session_id: Optional[str] = None,
        system_prompt: Optional[str] = None,
        temperature: Optional[float] = None,
        max_tokens: Optional[int] = None
    ) -> Iterator[str]:
        """
        Send a chat request and stream the response (if server supports streaming)

        Note: sigoREST currently returns complete responses. This method is a
        placeholder for future streaming support.

        Args:
            model: Model shortcode or full ID
            message: The user message
            session_id: Optional session ID
            system_prompt: Optional system prompt
            temperature: Optional temperature
            max_tokens: Optional max tokens

        Yields:
            Chunks of the response as they arrive
        """
        # For now, just yield the complete response
        # Future: Implement SSE streaming when sigoREST supports it
        response = self.chat(
            model=model,
            message=message,
            session_id=session_id,
            system_prompt=system_prompt,
            temperature=temperature,
            max_tokens=max_tokens
        )
        yield response.content

    def get_memory(self) -> Dict[str, Any]:
        """Get the global memory block"""
        return self._request("GET", "/api/memory")

    def set_memory(self, content: str, cache: bool = True) -> Dict[str, Any]:
        """
        Set the global memory block

        Args:
            content: The system context/prompt
            cache: Whether to use prompt caching
        """
        return self._request(
            "PUT",
            "/api/memory",
            headers={"Content-Type": "application/json"},
            json={"content": content, "cache": cache}
        )

    def close(self):
        """Close the HTTP session"""
        self._session.close()

    def __enter__(self):
        """Context manager entry"""
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        """Context manager exit"""
        self.close()
