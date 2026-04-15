#!/usr/bin/env python3
"""
Session-based conversation example

This example shows how to use sessions to maintain conversation context.
"""

from sigoclient import SigoClient, SigoError

def main():
    client = SigoClient("http://127.0.0.1:9080")

    if not client.ping():
        print("❌ sigoREST server is not responding")
        return

    session_id = "python-example-session"
    model = "claude-h"

    print("💡 Session-based conversation example")
    print(f"   Session: {session_id}")
    print(f"   Model: {model}")
    print()

    # First message
    try:
        print("👤 User: My name is Alice and I love Python.")
        response = client.chat(
            model=model,
            message="My name is Alice and I love Python.",
            session_id=session_id
        )
        print(f"🤖 Assistant: {response.content}")
        print()

        # Second message - context is preserved via session
        print("👤 User: What's my name and what do I like?")
        response = client.chat(
            model=model,
            message="What's my name and what do I like?",
            session_id=session_id
        )
        print(f"🤖 Assistant: {response.content}")
        print()

        # Third message - continue the conversation
        print("👤 User: Can you recommend a Python library for HTTP requests?")
        response = client.chat(
            model=model,
            message="Can you recommend a Python library for HTTP requests?",
            session_id=session_id,
            system_prompt="You are a helpful Python expert. Be concise."
        )
        print(f"🤖 Assistant: {response.content}")

    except SigoError as e:
        print(f"❌ Error: {e}")

if __name__ == "__main__":
    main()
