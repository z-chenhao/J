#!/usr/bin/env python3
"""Replay J-agent model frames through local MLX and apply a fitted J-lens."""

from __future__ import annotations

import hashlib
import json
import sys
from pathlib import Path
from typing import Any

import mlx.core as mx
import numpy as np
from mlx_lm.models.base import create_attention_mask, create_ssm_mask
from omlx.utils.model_loading import load_text_model

from torch_checkpoint import load_jacobian_lens


TOP_K = 5
MAX_SEQUENCE = 8192


def fail(message: str) -> None:
    raise ValueError(message)


def text_of(message: dict[str, Any], kind: str) -> str:
    return "".join(
        block.get("text", "")
        for block in message.get("content", [])
        if block.get("type") == kind
    )


def chat_message(message: dict[str, Any]) -> dict[str, Any]:
    role = message.get("role")
    if role not in {"system", "user", "assistant", "tool"}:
        fail(f"unsupported message role {role!r}")
    converted: dict[str, Any] = {
        "role": role,
        "content": text_of(message, "text"),
    }
    reasoning = text_of(message, "reasoning")
    if reasoning:
        converted["reasoning_content"] = reasoning
    calls = []
    for block in message.get("content", []):
        if block.get("type") != "tool_call":
            continue
        call = block.get("toolCall") or {}
        arguments = call.get("arguments", {})
        if not isinstance(arguments, str):
            arguments = json.dumps(arguments, ensure_ascii=False, separators=(",", ":"))
        calls.append(
            {
                "id": call.get("id", ""),
                "type": "function",
                "function": {
                    "name": call.get("name", ""),
                    "arguments": arguments,
                },
            }
        )
    if calls:
        converted["tool_calls"] = calls
    if role == "tool":
        converted["tool_call_id"] = message.get("toolCallId", "")
        if message.get("toolName"):
            converted["name"] = message["toolName"]
    return converted


def chat_tools(specifications: list[dict[str, Any]]) -> list[dict[str, Any]]:
    return [
        {
            "type": "function",
            "function": {
                "name": tool.get("name", ""),
                "description": tool.get("description", ""),
                "parameters": tool.get("inputSchema") or {"type": "object"},
            },
        }
        for tool in specifications
    ]


def tokens_for_frame(tokenizer: Any, frame: dict[str, Any]) -> tuple[list[int], list[int]]:
    request = frame.get("request") or {}
    response = frame.get("response") or {}
    request_messages = [chat_message(message) for message in request.get("messages", [])]
    messages = list(request_messages)
    assistant = response.get("message")
    if assistant:
        messages.append(chat_message(assistant))
    tools = chat_tools(request.get("tools", []))
    kwargs: dict[str, Any] = {
        "tokenize": True,
        "add_generation_prompt": False,
    }
    if tools:
        kwargs["tools"] = tools
    ids = tokenizer.apply_chat_template(messages, **kwargs)
    if hasattr(ids, "tolist"):
        ids = ids.tolist()
    if ids and isinstance(ids[0], list):
        ids = ids[0]
    if not isinstance(ids, list) or not all(isinstance(token, int) for token in ids):
        fail("tokenizer did not return a token id list")
    prefix_kwargs = dict(kwargs)
    prefix_kwargs["add_generation_prompt"] = True
    prefix = tokenizer.apply_chat_template(request_messages, **prefix_kwargs)
    if hasattr(prefix, "tolist"):
        prefix = prefix.tolist()
    if prefix and isinstance(prefix[0], list):
        prefix = prefix[0]
    common = 0
    while common < min(len(prefix), len(ids)) and prefix[common] == ids[common]:
        common += 1
    if len(ids) > MAX_SEQUENCE:
        removed = len(ids) - MAX_SEQUENCE
        ids = ids[-MAX_SEQUENCE:]
        common = max(0, common - removed)
    special = set(getattr(tokenizer, "all_special_ids", []))
    response_positions = [
        index for index in range(common, len(ids)) if ids[index] not in special
    ]
    if not response_positions:
        response_positions = list(range(common, len(ids))) or list(range(len(ids)))
    return ids, response_positions


def resolve_model(model: Any) -> tuple[Any, Any, list[Any], Any, Any]:
    text = getattr(model, "language_model", model)
    decoder = getattr(text, "model", None)
    if decoder is None:
        fail("loaded model has no text decoder")
    layers = list(getattr(decoder, "pipeline_layers", getattr(decoder, "layers", [])))
    embedding = getattr(decoder, "embed_tokens", None)
    norm = getattr(decoder, "norm", None)
    if not layers or embedding is None or norm is None:
        fail("loaded model does not expose the required residual stack")
    head = getattr(text, "lm_head", None)
    if head is None and not hasattr(embedding, "as_linear"):
        fail("loaded model has no unembedding")
    return text, decoder, layers, embedding, norm


def unembed(text: Any, embedding: Any, norm: Any, residual: mx.array) -> mx.array:
    normalized = norm(residual)
    head = getattr(text, "lm_head", None)
    return head(normalized) if head is not None else embedding.as_linear(normalized)


def chosen_positions(candidates: list[int], maximum: int) -> list[int]:
    if len(candidates) <= maximum:
        return candidates
    # The Agent-facing question is what is active while the answer is being
    # formed, so the current contract intentionally samples the response tail.
    return candidates[-maximum:]


def region(layer: int, count: int) -> str:
    fraction = layer / max(1, count - 1)
    if fraction < 0.3:
        return "early"
    if fraction < 0.78:
        return "workspace"
    return "late"


def top_concepts(logits: mx.array, tokenizer: Any) -> list[list[dict[str, Any]]]:
    vocab = logits.shape[-1]
    indices = mx.argpartition(logits, kth=vocab - TOP_K, axis=-1)[..., -TOP_K:]
    values = mx.take_along_axis(logits, indices, axis=-1)
    order = mx.argsort(values, axis=-1)[..., ::-1]
    indices = mx.take_along_axis(indices, order, axis=-1)
    values = mx.take_along_axis(values, order, axis=-1)
    mx.eval(indices, values)
    ids = np.asarray(indices).tolist()
    scores = np.asarray(values.astype(mx.float32)).tolist()
    rows = []
    for token_ids, token_scores in zip(ids, scores):
        concepts = []
        for rank, (token_id, score) in enumerate(zip(token_ids, token_scores)):
            decoded = tokenizer.decode([int(token_id)], skip_special_tokens=False)
            concepts.append(
                {
                    "token": decoded if decoded.strip() else f"<token:{int(token_id)}>",
                    "rank": rank + 1,
                    "score": round(float(score), 4),
                }
            )
        rows.append(concepts)
    return rows


def sha256(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(8 << 20), b""):
            digest.update(chunk)
    return digest.hexdigest()


def measure(payload: dict[str, Any]) -> dict[str, Any]:
    if payload.get("schemaVersion") != "jspace.trace.v0.1":
        fail("unsupported probe input schema")
    model_path = Path(payload.get("modelPath", ""))
    lens_path = Path(payload.get("lensPath", ""))
    if not model_path.is_dir():
        fail(f"model checkpoint not found: {model_path}")
    if not lens_path.is_file():
        fail(f"Jacobian lens not found: {lens_path}")
    frames = payload.get("frames") or []
    if not frames:
        fail("no model frames were provided")
    tail = int(payload.get("tailPositions", 18))

    lens, jacobians, prompts, width = load_jacobian_lens(lens_path)
    try:
        model, tokenizer = load_text_model(str(model_path))
        text, decoder, layers, embedding, norm = resolve_model(model)
        if width != int(getattr(text.args, "hidden_size", getattr(text.args, "text_config", {}).get("hidden_size", 0))):
            # oMLX's qwen wrapper stores TextModelArgs on language_model.args.
            actual = int(getattr(text, "args", None).hidden_size)
            if width != actual:
                fail(f"lens width {width} does not match checkpoint width {actual}")
        if sorted(jacobians) != list(range(len(layers) - 1)):
            fail("lens layers do not match the loaded checkpoint")

        turns = []
        for turn_index, frame in enumerate(frames):
            token_ids, response_positions = tokens_for_frame(tokenizer, frame)
            length = len(token_ids)
            selected = chosen_positions(response_positions, tail)
            inputs = mx.array([token_ids], dtype=mx.int32)
            hidden = embedding(inputs)
            fa_mask = create_attention_mask(hidden, None)
            ssm_mask = create_ssm_mask(hidden, None)
            positions: list[dict[str, Any]] = [
                {
                    "index": index - length,
                    "token": tokenizer.decode([token_ids[index]], skip_special_tokens=False),
                    "role": "assistant",
                    "layers": [],
                }
                for index in selected
            ]

            for layer_index, block in enumerate(layers):
                mask = ssm_mask if getattr(block, "is_linear", False) else fa_mask
                hidden = block(hidden, mask=mask, cache=None)
                residual = hidden[0, selected, :].astype(mx.float32)
                if layer_index in jacobians:
                    matrix = mx.array(lens.array(jacobians[layer_index]), dtype=mx.float32)
                    residual = residual @ matrix.T
                logits = unembed(text, embedding, norm, residual)
                concepts = top_concepts(logits, tokenizer)
                for position, top in zip(positions, concepts):
                    position["layers"].append(
                        {
                            "layer": layer_index,
                            "region": region(layer_index, len(layers)),
                            "top": top,
                        }
                    )
                mx.eval(hidden)
                if layer_index in jacobians:
                    del matrix

            turns.append({"index": turn_index, "selectedPositions": positions})

        configuration = getattr(text, "args", None)
        vocab = int(getattr(configuration, "vocab_size", 0))
        return {
            "measurement": {
                "kind": "posthoc_replay",
                "probe": "mlx-jacobian-lens-v0.1",
                "modelCheckpoint": payload.get("modelRepository", ""),
                "runtimeQuantization": "oQ4e/oQ5e mixed affine",
                "lensRepository": payload.get("lensRepository", ""),
                "lensSha256": sha256(lens_path),
                "contextFidelity": (
                    "exact J-agent messages and tool schemas; post-hoc full-prefix replay; "
                    "same local quantized checkpoint; separate MLX process; "
                    "MTP/cache kernel numerics may differ"
                ),
                "layers": len(layers),
                "residualWidth": width,
                "vocabularySize": vocab,
            },
            "turns": turns,
            "notes": [
                f"lens fitted from {prompts} prompts",
                "prefitted unquantized-model lens applied to the local mixed-quantized checkpoint",
                f"tail sampling kept at most {tail} token positions per model turn",
            ],
        }
    finally:
        lens.close()


def main() -> None:
    payload = json.load(sys.stdin)
    result = measure(payload)
    json.dump(result, sys.stdout, ensure_ascii=False, separators=(",", ":"))
    sys.stdout.write("\n")


if __name__ == "__main__":
    main()
