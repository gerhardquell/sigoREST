#!/usr/bin/env python3
"""
List all available models using the modern sigo_client.
"""

from sigo_client import SigoClient


def main():
    client = SigoClient("http://127.0.0.1:9080")

    if not client.ping():
        print("❌ sigoREST server is not responding on http://127.0.0.1:9080")
        print("   Start it with: ./sigoREST/sigoREST -q")
        return

    print("📋 sigoREST Models (v2 client)\n")
    print(f"✅ Connected | Server Status: OK\n")

    health = client.health()
    print(f"Available Models : {health.available_models}")
    print(f"Memory configured: {health.memory_set}")
    print("-" * 60)

    models = client.list_models()

    # Group by family
    from collections import defaultdict
    groups = defaultdict(list)

    for model in models:
        if "claude" in model.id.lower() or model.shortcode.startswith("cl"):
            groups["Claude"].append(model)
        elif any(x in model.id.lower() for x in ["kimi", "moonshot"]):
            groups["Moonshot / Kimi"].append(model)
        elif "ollama" in model.id.lower():
            groups["Ollama (Local)"].append(model)
        else:
            groups["Other"].append(model)

    for group_name, model_list in sorted(groups.items()):
        print(f"\n🔹 {group_name} ({len(model_list)} models)")
        print(f"{'Shortcode':<12} {'ID':<35} {'Input':>6} {'Output':>6}")
        print("-" * 65)
        for m in sorted(model_list, key=lambda x: x.shortcode or x.id):
            short = m.shortcode or m.id.split('/')[-1][-8:]
            print(f"{short:<12} {m.id:<35} ${m.input_cost:>5.2f} ${m.output_cost:>5.2f}")
        print()


if __name__ == "__main__":
    main()
