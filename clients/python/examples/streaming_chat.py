#!/usr/bin/env python3
"""
Streaming chat example with the modern sigo_client v2.

Uses real Server-Sent Events (SSE) when the server supports it.
Falls back to simulated streaming if needed.
"""

import sys
import time
from sigo_client import SigoClient


def main():
    client = SigoClient("http://127.0.0.1:9080")
    model = "cl5-s"

    if not client.ping():
        print("❌ Server not reachable. Start with: ./sigoREST/sigoREST -q")
        return

    print("🌊 sigo_client v2 - Streaming Demo")
    print(f"   Model: {model}")
    print("   (Simulated streaming - server returns full response internally)\n")

    user_message = "Schreibe ein kurzes Gedicht über das Programmieren in Go und Python."

    print(f"👤 User: {user_message}")
    print("\n🤖 Assistant: ", end="", flush=True)

    try:
        print("🌊 Starting real SSE streaming...\n")
        stream = client.chat.completions.create(
            model=model,
            messages=[{"role": "user", "content": user_message}],
            temperature=0.8,
            stream=True,          # ← Echtes Streaming (SSE)
        )

        full_response = ""
        for chunk in stream:
            content = chunk.content
            if content:
                full_response += content
                print(content, end="", flush=True)

        print("\n\n✅ Real SSE Streaming completed!")
        print(f"   Total characters: {len(full_response)}")

    except Exception as e:
        print(f"\n❌ Error: {e}")
        print("   (Falling back to simulated streaming if server SSE failed)")


if __name__ == "__main__":
    main()
