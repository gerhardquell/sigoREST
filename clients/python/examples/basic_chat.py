#!/usr/bin/env python3
"""
Basic chat example with sigoclient

This example shows how to send a simple chat message to sigoREST.
"""

from sigoclient import SigoClient, SigoError

def main():
    # Create client (connects to localhost:9080 by default)
    client = SigoClient("http://127.0.0.1:9080")

    # Check if server is alive
    if not client.ping():
        print("❌ sigoREST server is not responding")
        print("   Make sure the server is running: ./sigoREST/sigoREST -q")
        return

    print("✅ Connected to sigoREST")
    print()

    # Simple chat
    try:
        response = client.chat(
            model="claude-h",
            message="Explain quantum computing in one sentence."
        )
        print(f"🤖 Model: {response.model}")
        print(f"💬 Response: {response.content}")
    except SigoError as e:
        print(f"❌ Error: {e}")

if __name__ == "__main__":
    main()
