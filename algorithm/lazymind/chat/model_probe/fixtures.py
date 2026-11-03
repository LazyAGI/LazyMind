"""Small deterministic inputs; no user files, downloads or temporary artifacts."""
import base64
import io
import struct
import wave
import zlib


def _png_chunk(name: bytes, value: bytes) -> bytes:
    return struct.pack('>I', len(value)) + name + value + struct.pack('>I', zlib.crc32(name + value))


def _image() -> bytes:
    # A small, valid square RGB image works as a visual/API input, not a quality benchmark.
    header = struct.pack('>IIBBBBB', 32, 32, 8, 2, 0, 0, 0)
    pixels = (b'\x00' + b'\x30\x70\xb0' * 32) * 32
    return (b'\x89PNG\r\n\x1a\n' + _png_chunk(b'IHDR', header)
            + _png_chunk(b'IDAT', zlib.compress(pixels)) + _png_chunk(b'IEND', b''))


def _audio() -> bytes:
    # Silence tests the transcription endpoint's acceptance; an empty transcript is valid.
    output = io.BytesIO()
    with wave.open(output, 'wb') as audio:
        audio.setnchannels(1)
        audio.setsampwidth(2)
        audio.setframerate(16000)
        audio.writeframes(b'\x00\x00' * 4000)
    return output.getvalue()


IMAGE = _image()
IMAGE_DATA_URL = 'data:image/png;base64,' + base64.b64encode(IMAGE).decode('ascii')
AUDIO = _audio()
AUDIO_BASE64 = base64.b64encode(AUDIO).decode('ascii')
