"""Pydantic models for sigoREST responses."""

from datetime import datetime
from typing import List, Optional, Dict, Any, Literal
from pydantic import BaseModel, Field


class Usage(BaseModel):
    """Token usage information."""
    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0


class ChatCompletionMessage(BaseModel):
    """A message in a chat completion."""
    role: Literal["system", "user", "assistant"]
    content: str


class ChatCompletionChoice(BaseModel):
    """A choice in a chat completion response."""
    index: int
    message: ChatCompletionMessage
    finish_reason: Optional[str] = None


class ChatCompletion(BaseModel):
    """Response from a chat completion request."""
    id: str
    object: str = "chat.completion"
    created: int
    model: str
    choices: List[ChatCompletionChoice]
    usage: Optional[Usage] = None
    raw_response: Optional[Dict[str, Any]] = Field(default_factory=dict, exclude=True)

    @property
    def content(self) -> str:
        """Convenience property to get the main response content."""
        if self.choices and self.choices[0].message.content:
            return self.choices[0].message.content
        return ""


class ChatCompletionChunk(BaseModel):
    """Streaming chunk from a chat completion (OpenAI compatible)."""
    id: str
    object: str = "chat.completion.chunk"
    created: int
    model: str
    choices: List[Dict[str, Any]]

    @property
    def content(self) -> str:
        """Extract delta content from first choice."""
        if self.choices and isinstance(self.choices[0], dict):
            delta = self.choices[0].get("delta", {})
            if isinstance(delta, dict):
                return delta.get("content", "")
        return ""


class Model(BaseModel):
    """Information about an available model."""
    id: str
    shortcode: str = ""
    object: str = "model"
    owned_by: str = "sigoREST"
    input_cost: float = 0.0
    output_cost: float = 0.0
    max_input_tokens: int = 0
    max_output_tokens: int = 0
    endpoint: Optional[str] = None
    requires_completion_tokens: bool = False


class ModelList(BaseModel):
    """List of available models."""
    object: str = "list"
    data: List[Model]


class HealthResponse(BaseModel):
    """Server health status."""
    status: str
    timestamp: int
    available_models: int
    memory_set: bool = False
    circuit_breakers: Optional[List[Dict[str, Any]]] = None


class MemoryBlock(BaseModel):
    """Global memory/system prompt block."""
    content: str
    cache: bool = True
    updated_at: Optional[datetime] = None
