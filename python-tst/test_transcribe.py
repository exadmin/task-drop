from array import array
from pathlib import Path
import tempfile
import unittest
import wave

from transcribe import SAMPLE_RATE, SplitOptions, Transcriber, iter_chunks, main, write_chunk


def signal(seconds: float, value: int = 2000) -> bytes:
    return array("h", [value] * int(seconds * SAMPLE_RATE)).tobytes()


class SplitTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.root = Path(self.directory.name)

    def make_wav(self, name: str, data: bytes) -> Path:
        path = self.root / name
        write_chunk(path, data)
        return path

    def test_pause_cut_preserves_samples(self):
        data = signal(20) + signal(1, 0) + signal(35)
        chunks = list(iter_chunks(self.make_wav("long.wav", data)))
        self.assertTrue(20 < chunks[0].duration < 21)
        self.assertFalse(chunks[0].forced_cut)
        self.assertTrue(any(chunk.forced_cut for chunk in chunks))
        self.assertTrue(all(0 < chunk.duration <= 25 for chunk in chunks))
        self.assertEqual(b"".join(chunk.pcm for chunk in chunks), data)
        self.assertEqual(chunks[-1].start_frame + len(chunks[-1].pcm) // 2, len(data) // 2)

    def test_exact_limit_and_one_sample_over(self):
        for frames, count in ((25 * SAMPLE_RATE, 1), (25 * SAMPLE_RATE + 1, 2)):
            chunks = list(iter_chunks(self.make_wav(f"{frames}.wav", b"\x00\x08" * frames)))
            self.assertEqual(len(chunks), count)
            self.assertEqual(sum(len(chunk.pcm) // 2 for chunk in chunks), frames)

    def test_order_and_combined_text(self):
        class Model:
            def transcribe(self, path):
                with wave.open(path, "rb") as reader:
                    samples = array("h", reader.readframes(reader.getnframes()))
                    return str(samples[0])
        first = self.make_wav("z.wav", signal(1, 1000))
        second = self.make_wav("a.wav", signal(1, 2000))
        transcriber = Transcriber()
        transcriber._model = Model()
        result = transcriber.transcribe_files([first, second])
        self.assertEqual(result.text, "1000 2000")
        self.assertEqual([segment.source for segment in result.segments], [str(first), str(second)])

    def test_silence_needs_no_model(self):
        transcriber = Transcriber()
        result = transcriber.transcribe_files([self.make_wav("silence.wav", signal(51, 0))])
        self.assertEqual(result.text, "")
        self.assertIsNone(transcriber._model)
        self.assertTrue(all(segment.end - segment.start <= 25 for segment in result.segments))

    def test_invalid_format_and_truncated_data(self):
        bad = self.root / "stereo.wav"
        with wave.open(str(bad), "wb") as writer:
            writer.setnchannels(2)
            writer.setsampwidth(2)
            writer.setframerate(48000)
            writer.writeframes(b"\0" * 400)
        with self.assertRaisesRegex(ValueError, "mono PCM"):
            list(iter_chunks(bad))
        truncated = self.make_wav("truncated.wav", signal(1))
        truncated.write_bytes(truncated.read_bytes()[:-100])
        with self.assertRaisesRegex(ValueError, "truncated"):
            list(iter_chunks(truncated))

    def test_output_cannot_overwrite_input(self):
        path = self.make_wav("source.wav", signal(1, 0))
        original = path.read_bytes()
        self.assertEqual(main([str(path), "--output", str(path)]), 1)
        self.assertEqual(path.read_bytes(), original)

    def test_invalid_options(self):
        for value in (0, 26, float("nan")):
            with self.assertRaises(ValueError):
                SplitOptions(max_seconds=value)


if __name__ == "__main__":
    unittest.main()
