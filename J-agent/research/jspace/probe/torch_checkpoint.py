"""Restricted reader for the simple tensor dictionary emitted by torch.save.

The J-lens checkpoint is a zip file containing a pickle metadata stream and
raw tensor storage files.  Importing PyTorch solely to read those arrays would
add a large runtime dependency to this MLX-only research tool, so this module
implements the small, allowlisted subset of the format used by
JacobianLens.save().

It intentionally rejects arbitrary pickle globals and non-contiguous tensors.
"""

from __future__ import annotations

import collections
import io
import pickle
import zipfile
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import numpy as np


@dataclass(frozen=True)
class Storage:
    key: str
    dtype: np.dtype
    size: int


@dataclass(frozen=True)
class Tensor:
    storage: Storage
    offset: int
    shape: tuple[int, ...]
    stride: tuple[int, ...]


class _StorageType:
    def __init__(self, dtype: str):
        self.dtype = np.dtype(dtype)


def _rebuild_tensor(
    storage: Storage,
    storage_offset: int,
    size: tuple[int, ...],
    stride: tuple[int, ...],
    *_: Any,
) -> Tensor:
    return Tensor(storage, storage_offset, tuple(size), tuple(stride))


class _RestrictedUnpickler(pickle.Unpickler):
    _GLOBALS = {
        ("collections", "OrderedDict"): collections.OrderedDict,
        ("torch._utils", "_rebuild_tensor"): _rebuild_tensor,
        ("torch._utils", "_rebuild_tensor_v2"): _rebuild_tensor,
        ("torch", "HalfStorage"): _StorageType("<f2"),
        ("torch", "FloatStorage"): _StorageType("<f4"),
        ("torch", "DoubleStorage"): _StorageType("<f8"),
        ("torch", "LongStorage"): _StorageType("<i8"),
        ("torch", "IntStorage"): _StorageType("<i4"),
    }

    def find_class(self, module: str, name: str) -> Any:
        allowed = self._GLOBALS.get((module, name))
        if allowed is None:
            raise pickle.UnpicklingError(f"checkpoint global is not allowed: {module}.{name}")
        return allowed

    def persistent_load(self, identifier: Any) -> Storage:
        if not isinstance(identifier, tuple) or len(identifier) < 5:
            raise pickle.UnpicklingError("invalid storage identifier")
        kind, storage_type, key, _location, size = identifier[:5]
        if kind != "storage" or not isinstance(storage_type, _StorageType):
            raise pickle.UnpicklingError("unsupported persistent object")
        return Storage(str(key), storage_type.dtype, int(size))


class TorchZip:
    """Read-only, restricted view of a torch.save zip checkpoint."""

    def __init__(self, path: str | Path):
        self.path = Path(path)
        self.archive = zipfile.ZipFile(self.path, "r")
        names = self.archive.namelist()
        metadata = [name for name in names if name.endswith("/data.pkl")]
        if len(metadata) != 1:
            self.archive.close()
            raise ValueError("checkpoint must contain exactly one data.pkl")
        self.prefix = metadata[0][: -len("data.pkl")]
        unpickler = _RestrictedUnpickler(io.BytesIO(self.archive.read(metadata[0])))
        self.value = unpickler.load()

    def close(self) -> None:
        self.archive.close()

    def __enter__(self) -> "TorchZip":
        return self

    def __exit__(self, *_: Any) -> None:
        self.close()

    def array(self, tensor: Tensor) -> np.ndarray:
        if not isinstance(tensor, Tensor):
            raise TypeError("checkpoint value is not a tensor")
        expected_stride = []
        running = 1
        for dimension in reversed(tensor.shape):
            expected_stride.append(running)
            running *= dimension
        if tuple(reversed(expected_stride)) != tensor.stride:
            raise ValueError("non-contiguous tensors are not supported")
        raw = self.archive.read(f"{self.prefix}data/{tensor.storage.key}")
        storage = np.frombuffer(raw, dtype=tensor.storage.dtype)
        if storage.size != tensor.storage.size:
            raise ValueError("tensor storage length does not match metadata")
        count = int(np.prod(tensor.shape, dtype=np.int64))
        end = tensor.offset + count
        if tensor.offset < 0 or end > storage.size:
            raise ValueError("tensor view exceeds its storage")
        return storage[tensor.offset:end].reshape(tensor.shape)


def load_jacobian_lens(path: str | Path) -> tuple[TorchZip, dict[int, Tensor], int, int]:
    """Open and validate the dictionary written by JacobianLens.save()."""

    checkpoint = TorchZip(path)
    try:
        value = checkpoint.value
        if not isinstance(value, dict) or set(value) != {
            "J",
            "n_prompts",
            "source_layers",
            "d_model",
        }:
            raise ValueError("checkpoint is not an exact JacobianLens artifact")
        jacobians = value["J"]
        layers = value["source_layers"]
        width = int(value["d_model"])
        prompts = int(value["n_prompts"])
        if not isinstance(jacobians, dict) or sorted(jacobians) != list(layers):
            raise ValueError("Jacobian layer index is inconsistent")
        if width <= 0 or prompts <= 0:
            raise ValueError("invalid lens metadata")
        for layer, tensor in jacobians.items():
            if not isinstance(layer, int) or not isinstance(tensor, Tensor):
                raise ValueError("invalid Jacobian entry")
            if tensor.shape != (width, width):
                raise ValueError(f"layer {layer} has shape {tensor.shape}, expected {(width, width)}")
        return checkpoint, jacobians, prompts, width
    except Exception:
        checkpoint.close()
        raise
