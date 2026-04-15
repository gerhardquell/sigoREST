#!/usr/bin/env python3
"""
List available models example

This example shows how to get information about available models.
"""

from sigoclient import SigoClient

def main():
    client = SigoClient("http://127.0.0.1:9080")

    if not client.ping():
        print("❌ sigoREST server is not responding")
        return

    print("📋 Available Models\n")

    # Get health info
    health = client.health()
    print(f"Server Status: {health.get('status', 'unknown')}")
    print(f"Available Models: {health.get('available_models', 0)}")
    print(f"Memory Set: {health.get('memory_set', False)}")
    print()

    # List all models
    models = client.list_models()

    # Group by provider (simple heuristic)
    providers = {}
    for model in models:
        if "mammoth" in model.id.lower():
            provider = "Mammoth.ai"
        elif "moonshot" in model.id.lower() or model.id.startswith("kimi"):
            provider = "Moonshot"
        elif "z.ai" in model.id.lower():
            provider = "Z.ai"
        elif model.id.startswith("ollama-"):
            provider = "Ollama (Local)"
        else:
            provider = "Other"

        if provider not in providers:
            providers[provider] = []
        providers[provider].append(model)

    for provider, model_list in sorted(providers.items()):
        print(f"\n--- {provider} ---")
        print(f"{'ID':<30} {'Shortcode':<15} {'Input':>8} {'Output':>8}")
        print("-" * 65)
        for m in sorted(model_list, key=lambda x: x.shortcode):
            print(f"{m.id:<30} {m.shortcode:<15} ${m.input_cost:>6.2f} ${m.output_cost:>6.2f}")

if __name__ == "__main__":
    main()
