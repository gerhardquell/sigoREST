#!/usr/bin/env python3
"""
Session-based conversation example with the modern sigo_client.
Demonstrates conversation memory using session_id.
"""

from sigo_client import SigoClient, SigoError


def main():
    client = SigoClient("http://127.0.0.1:9080")
    session_id = "modern-python-demo"
    model = "cl5-s"

    if not client.ping():
        print("Server not running.")
        return

    print(f"💬 Session Conversation Demo")
    print(f"   Model     : {model}")
    print(f"   Session ID: {session_id}\n")

    try:
        # First message
        print("👤 User: Ich heiße Gerhard und entwickle AI-Tools.")
        resp1 = client.chat.completions.create(
            model=model,
            messages=[{"role": "user", "content": "Ich heiße Gerhard und entwickle AI-Tools."}],
            session_id=session_id,
        )
        print(f"🤖 Assistant: {resp1.content}\n")

        # Second message - context should be preserved
        print("👤 User: Wie heiße ich und was mache ich?")
        resp2 = client.chat.completions.create(
            model=model,
            messages=[{"role": "user", "content": "Wie heiße ich und was mache ich?"}],
            session_id=session_id,
        )
        print(f"🤖 Assistant: {resp2.content}\n")

        print("✅ Session memory works! The assistant remembered the name.")

    except SigoError as e:
        print(f"❌ Error: {e}")


if __name__ == "__main__":
    main()
