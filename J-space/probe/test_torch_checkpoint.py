import io
import pickle
import tempfile
import unittest
import zipfile
from pathlib import Path

from torch_checkpoint import _RestrictedUnpickler


class RestrictedCheckpointTest(unittest.TestCase):
    def test_rejects_arbitrary_pickle_global(self):
        payload = pickle.dumps(Path("/tmp/not-allowed"))
        with self.assertRaises(pickle.UnpicklingError):
            _RestrictedUnpickler(io.BytesIO(payload)).load()

    def test_archive_requires_metadata(self):
        from torch_checkpoint import TorchZip

        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory) / "bad.pt"
            with zipfile.ZipFile(path, "w") as archive:
                archive.writestr("bad/value.txt", "no pickle")
            with self.assertRaises(ValueError):
                TorchZip(path)


if __name__ == "__main__":
    unittest.main()
