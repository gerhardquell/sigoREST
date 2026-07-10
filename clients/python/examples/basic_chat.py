#!/usr/bin/env python3
"""
Basic chat example using the modern OpenAI-compatible sigo_client v2.
"""

from sigo_client import SigoClient, SigoError


def main():
    client = SigoClient("http://127.0.0.1:9080")

    if not client.ping():
        print("❌ Server not reachable. Start with: ./sigoREST/sigoREST -q")
        return

    print("🚀 sigo_client v2 - Basic Chat Example\n")

    try:
        response = client.chat.completions.create(
            model="cl5-s",  # Current Claude Sonnet 5 shortcode
            messages=[
                {"role": "system", "content": "Du bist ein hilfreicher, präziser Assistent."},
                {"role": "user", "content": "Erkläre in einem Satz, was sigoREST ist."}
            ],
            temperature=0.7,
            max_tokens=200,
        )

        print(f"✅ Model     : {response.model}")
        print(f"💬 Response  : {response.content}")
        print(f"📊 Tokens    : {response.usage.total_tokens if response.usage else 'N/A'}")

    except SigoAPIError as e:
        print(f"❌ API Error {e.status_code}: {e}")
    except SigoError as e:
        print(f"❌ Client Error: {e}")
    except Exception as e:
        print(f"❌ Unexpected error: {e}")


if __name__ == "__main__":
    main()
