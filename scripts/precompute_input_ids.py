#!/usr/bin/env python3
"""Pre-compute input_ids for all SigLIP2 prompts.
Generates models/siglip2-512/input_ids.json — loaded by the Inference Service at startup.

This avoids needing a runtime Rust tokenizer in Go.
Run whenever prompts are added or changed in inference/config.yaml.

Usage:
    .venv/bin/python scripts/precompute_input_ids.py
"""
import json
import sys
from transformers import AutoTokenizer

MODEL_ID = "google/siglip2-so400m-patch16-512"
OUTPUT_PATH = "models/siglip2-512/input_ids.json"

# Prompts must match inference/config.yaml exactly.
# Categories listed in the SAME ORDER as the config.
CATEGORIES = {
    "sexual": [
        "this is a photo of sexual or adult content.",
        "this is a photo of nudity or sexual acts.",
        "this is a photo of explicit adult content.",
        "this is a photo of pornographic material.",
        "this is a photo of intimate body parts.",
    ],
    "violence": [
        "this is a photo of violence or gore.",
        "this is a photo of physical harm or blood.",
        "this is a photo of graphic violence.",
        "this is a photo of someone being injured.",
    ],
    "hate": [
        "this is a photo of hate symbols or speech.",
        "this is a photo of racist or discriminatory content.",
        "this is a photo of nazi or extremist symbols.",
        "this is a photo of hateful imagery targeting a group.",
        "this is a photo of white supremacy or fascism symbols.",
    ],
    "harassment": [
        "this is a photo of harassment or bullying.",
        "this is a photo of cyberbullying.",
        "this is a photo intended to intimidate or mock.",
        "this is a photo of doxxing or exposing personal information.",
    ],
    "self_harm": [
        "this is a photo of self-harm or suicide.",
        "this is a photo of self-injury.",
        "this is a photo of content promoting self-harm.",
        "this is a photo of someone cutting themselves.",
    ],
    "sexual_minors": [
        "this is a photo of a minor in sexual context.",
        "this is a photo of child sexual abuse material.",
        "this is a photo of inappropriate content involving minors.",
        "this is a photo of a child in suggestive pose.",
    ],
}


def main():
    print(f"Loading tokenizer: {MODEL_ID}")
    tokenizer = AutoTokenizer.from_pretrained(MODEL_ID)

    input_ids_map = {}
    total_prompts = 0
    max_len = 0

    for cat_name, prompts in CATEGORIES.items():
        input_ids_map[cat_name] = []
        for prompt in prompts:
            ids = tokenizer.encode(prompt, add_special_tokens=True)
            input_ids_map[cat_name].append(ids)
            total_prompts += 1
            max_len = max(max_len, len(ids))

    output = {
        "model_id": MODEL_ID,
        "total_prompts": total_prompts,
        "max_token_length": max_len,
        "categories": input_ids_map,
    }

    with open(OUTPUT_PATH, "w") as f:
        json.dump(output, f, indent=2)

    print(f"Saved {total_prompts} prompts × {len(CATEGORIES)} categories → {OUTPUT_PATH}")
    print(f"Max token length: {max_len}")

    for cat_name, ids_list in input_ids_map.items():
        lens = [len(ids) for ids in ids_list]
        print(f"  {cat_name}: {len(ids_list)} prompts, lengths {lens}")

    # Verify all categories are present
    if len(input_ids_map) != len(CATEGORIES):
        print("ERROR: category count mismatch!", file=sys.stderr)
        sys.exit(1)

    return 0


if __name__ == "__main__":
    sys.exit(main())
