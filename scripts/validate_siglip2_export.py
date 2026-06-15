#!/usr/bin/env python3
"""Validate SigLIP2 ONNX export accuracy vs PyTorch FP32 reference.

Usage:
    .venv/bin/python scripts/validate_siglip2_export.py

Gate: cosine similarity > 0.95 for image embeddings, text embeddings, and logits.
"""
import sys
import numpy as np
import onnxruntime as ort
from transformers import AutoModel, AutoProcessor
import torch

MODEL_ID = "google/siglip2-so400m-patch16-512"
ONNX_PATH = "models/siglip2-512/vision/model.onnx"
GATE = 0.95  # Minimum cosine similarity


def cosine_sim(a: np.ndarray, b: np.ndarray) -> float:
    return float(np.dot(a, b) / (np.linalg.norm(a) * np.linalg.norm(b)))


def main():
    # Load PyTorch reference (FP32)
    print("Loading PyTorch reference...")
    pt_model = AutoModel.from_pretrained(MODEL_ID, dtype=torch.float32)
    pt_model.eval()

    # Load ONNX Runtime (CPU)
    print("Loading ONNX Runtime...")
    ort_session = ort.InferenceSession(ONNX_PATH, providers=["CPUExecutionProvider"])

    # Create test data
    rng = np.random.RandomState(42)
    test_image = rng.randint(0, 255, (512, 512, 3), dtype=np.uint8)
    test_texts = [
        "this is a photo of sexual or adult content.",
        "this is a photo of a cat.",
        "this is a photo of violence or gore.",
    ]

    # PyTorch forward pass
    processor = AutoProcessor.from_pretrained(MODEL_ID)
    inputs = processor(
        text=test_texts,
        images=test_image,
        return_tensors="pt",
        padding="max_length",
        truncation=True,
    )

    with torch.no_grad():
        pt_outputs = pt_model(**inputs)

    pt_image = pt_outputs.image_embeds.cpu().numpy()
    pt_text = pt_outputs.text_embeds.cpu().numpy()
    pt_logits = pt_outputs.logits_per_image.cpu().numpy()

    # ONNX Runtime forward pass
    ort_inputs = {
        "pixel_values": inputs["pixel_values"].cpu().numpy().astype(np.float32),
        "input_ids": inputs["input_ids"].cpu().numpy().astype(np.int64),
    }
    ort_outputs = ort_session.run(None, ort_inputs)
    ort_names = [o.name for o in ort_session.get_outputs()]
    ort = dict(zip(ort_names, ort_outputs))

    # Compare
    img_cos = cosine_sim(pt_image[0], ort["image_embeds"][0])
    logits_cos = cosine_sim(pt_logits.flatten(), ort["logits_per_image"].flatten())

    print(f"\nImage embeds cosine similarity: {img_cos:.6f} ({'PASS' if img_cos > GATE else 'FAIL'})")
    print(f"Logits cosine similarity:     {logits_cos:.6f} ({'PASS' if logits_cos > GATE else 'FAIL'})")

    for i in range(len(test_texts)):
        txt_cos = cosine_sim(pt_text[i], ort["text_embeds"][i])
        print(f"Text embeds [{i}] cosine:     {txt_cos:.6f} ({'PASS' if txt_cos > GATE else 'FAIL'})")

    print(f"\nMax abs diff (image): {np.max(np.abs(pt_image - ort['image_embeds'])):.2e}")
    print(f"Max abs diff (text):  {np.max(np.abs(pt_text - ort['text_embeds'])):.2e}")

    all_pass = img_cos > GATE and logits_cos > GATE
    print(f"\nGate > {GATE}: {'PASS' if all_pass else 'FAIL'}")
    sys.exit(0 if all_pass else 1)


if __name__ == "__main__":
    main()
