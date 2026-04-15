"""
sigoclient - Python client for sigoREST API

A simple, lightweight client for the sigoREST OpenAI-compatible API.
"""

from .client import SigoClient, SigoError, SigoAPIError

__version__ = "1.0.0"
__all__ = ["SigoClient", "SigoError", "SigoAPIError"]
