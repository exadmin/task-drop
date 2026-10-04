from io import StringIO
import json
from types import SimpleNamespace
import unittest

from service import serve


class ServiceTests(unittest.TestCase):
    def test_resident_model_errors_and_order(self):
        class Model:
            loads = 0
            def _load_model(self):
                self.loads += 1
                print("model progress belongs in stderr")
            def transcribe_files(self, paths):
                if paths == ["bad.wav"]:
                    raise ValueError("bad WAV")
                return SimpleNamespace(text="Результат: " + ",".join(paths))
        model = Model()
        requests = [
            {"id": "1", "wav_files": ["a.wav", "b.wav"]},
            {"id": "2", "wav_files": ["bad.wav"]},
            {"id": "3", "wav_files": ["c.wav"]},
        ]
        output = StringIO()
        serve(model, StringIO("\n".join(json.dumps(r) for r in requests)), output)
        replies = [json.loads(line) for line in output.getvalue().splitlines()]
        self.assertEqual(model.loads, 1)
        self.assertEqual([r["type"] for r in replies], ["ready", "result", "error", "result"])
        self.assertEqual([r["id"] for r in replies[1:]], ["1", "2", "3"])
        self.assertEqual(replies[1]["text"], "Результат: a.wav,b.wav")


if __name__ == "__main__":
    unittest.main()
