#!/usr/bin/env python3
"""
Async chat example using AsyncSigoClient.
"""

import asyncio
from sigo_client import AsyncSigoClient, SigoError


async def main():
    async with AsyncSigoClient("http://127.0.0.1:9080") as client:
        if not await client.ping():
            print("❌ Server not reachable")
            return

        print("⚡ Async sigo_client v2 Example\n")

        try:
            print("⚡ Starting async real SSE streaming...\n")
            stream = await client.chat.completions.create(
                model="cl5-s",
                messages=[{"role": "user", "content": "Schreibe einen kurzen Haiku über KI auf Deutsch."}],
                temperature=0.8,
                stream=True,
            )

            print("🤖 Assistant: ", end="", flush=True)
            full_response = ""
            async for chunk in stream:
                content = getattr(chunk, 'content', '')
                if content:
                    full_response += content
                    print(content, end="", flush=True)
                    await asyncio.sleep(0.015)

            print("\n\n✅ Async SSE Streaming completed!")
            print(f"   Total characters: {len(full_response)}")
        except SigoError as e:
            print(f"❌ Error: {e}")


if __name__ == "__main__":
    asyncio.run(main())
