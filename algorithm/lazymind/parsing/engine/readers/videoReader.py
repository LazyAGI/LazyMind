import os
import shutil
import subprocess
import tempfile
from pathlib import Path
from typing import List, Optional

from lazyllm import LOG
from lazyllm.thirdparty import fsspec
from lazyllm.tools.rag.doc_node import DocNode, ImageDocNode
from lazyllm.tools.rag.readers.readerBase import LazyLLMReaderBase, get_default_fs, is_default_fs

from lazymind.config import config as _cfg
from lazymind.parsing.engine.readers.imageEmbReader import ImageEmbReader

FRAME_DIR = Path(tempfile.gettempdir()) / 'lazymind_video_frames'


class _WhisperMediaReader(LazyLLMReaderBase):
    '''MP4→MP3 + Whisper; MP3 direct. Used only by VideoReader.'''

    __lazyllm_registry_disable__ = True

    def __init__(
        self, model_version: Optional[str] = None, return_trace: bool = True,
        time_segment: bool = False, time_interval: Optional[int] = None,
    ) -> None:
        super().__init__(return_trace=return_trace)
        self._model_version = model_version or _cfg['whisper_model']
        self._time_segment = time_segment
        self._time_interval = time_interval if time_interval is not None else _cfg['audio_segment_interval']
        self._model = None

    def _get_model(self):
        if self._model is None:
            try:
                import whisper
            except ImportError as exc:
                raise ImportError(
                    'Please install OpenAI whisper model `pip install openai-whisper` to use the model'
                ) from exc
            self._model = whisper.load_model(self._model_version)
        return self._model

    def __getstate__(self):
        state = self.__dict__.copy()
        state['_model'] = None
        return state

    def _load_data(
        self, file: Path,
        fs: Optional['fsspec.AbstractFileSystem'] = None,
    ) -> List[DocNode]:
        if not isinstance(file, Path):
            file = Path(file)

        video_input = False
        video_file_path = None
        temp_audio_file = None
        if file.suffix.lower() == '.mp4':
            try:
                from pydub import AudioSegment
            except ImportError as exc:
                raise ImportError('Please install pydub `pip install pydub`') from exc

            if fs:
                with fs.open(file, 'rb') as f:
                    video = AudioSegment.from_file(f, format='mp4')
            else:
                video = AudioSegment.from_file(file, format='mp4')

            video_input = True
            audio = video
            video_file_path = file
            with tempfile.NamedTemporaryFile(suffix='.mp3', delete=False) as tmp_file:
                temp_audio_file = Path(tmp_file.name)
            file = temp_audio_file
            audio.export(str(file), format='mp3')

        model = self._get_model()
        metadata_audio_path = video_file_path if video_input and video_file_path is not None else file

        try:
            if self._time_segment:
                result = model.transcribe(str(file), word_timestamps=True)
                return self._merge_segments(result['segments'], metadata_audio_path, video_input, video_file_path)

            result = model.transcribe(str(file))
            transcript = result['text']
            metadata = {
                'start_time': 0,
                'end_time': -1,
                'audio_file_path': str(metadata_audio_path),
                'multimodal_type': 'video_audio_text',
            }
            if video_input:
                metadata['video_file_path'] = str(video_file_path)
            return [DocNode(text=transcript, metadata=metadata)]
        finally:
            if temp_audio_file is not None:
                temp_audio_file.unlink(missing_ok=True)

    def _merge_segments(
        self, segments, metadata_audio_path: Path, video_input: bool = False,
        video_file_path: Optional[Path] = None,
    ) -> List[DocNode]:
        nodes = []
        merged_text = []
        merged_start = None
        merged_end = None

        def _build_node(start_time, end_time, texts):
            metadata = {
                'start_time': start_time,
                'end_time': end_time,
                'audio_file_path': str(metadata_audio_path),
                'multimodal_type': 'video_audio_text',
            }
            if video_input and video_file_path is not None:
                metadata['video_file_path'] = str(video_file_path)
            return DocNode(text=''.join(texts), metadata=metadata)

        for segment in segments:
            start_time = segment['start']
            end_time = segment['end']
            text = segment['text']

            if merged_start is None:
                merged_start = start_time
                merged_end = end_time
                merged_text.append(text)
                continue

            if end_time - merged_start < self._time_interval:
                merged_end = end_time
                merged_text.append(text)
                continue

            nodes.append(_build_node(merged_start, merged_end, merged_text))
            merged_start = start_time
            merged_end = end_time
            merged_text = [text]

        if merged_start is not None:
            nodes.append(_build_node(merged_start, merged_end, merged_text))

        return nodes


class VideoReader(LazyLLMReaderBase):
    '''MP3: speech-to-text transcript. MP4: same plus interval frame extraction.

    When ``model_role`` is set, resolve it via ``AutoModel`` for transcription.
    Otherwise fall back to local Whisper. Without either (or on failure), MP4 still
    yields frame image nodes; MP3 yields no nodes.
    '''

    __lazyllm_registry_disable__ = True

    def __init__(
        self,
        frame_interval: Optional[float] = None,
        model_version: Optional[str] = None,
        model_role: Optional[str] = None,
        return_trace: bool = True,
        time_segment: bool = True,
        time_interval: Optional[int] = None,
    ) -> None:
        super().__init__(return_trace=return_trace)
        interval = frame_interval if frame_interval is not None else float(_cfg['video_frame_interval'])
        if interval <= 0:
            raise ValueError('`frame_interval` must be greater than 0.')
        self._frame_interval = interval
        self._model_role = model_role
        self._stt_model = None
        self._audio_reader = None
        if not model_role:
            self._audio_reader = _WhisperMediaReader(
                model_version=model_version,
                return_trace=return_trace,
                time_segment=time_segment,
                time_interval=time_interval,
            )
        self._image_reader = ImageEmbReader(return_trace=return_trace)

    def _get_stt_model(self):
        if self._stt_model is None and self._model_role:
            from lazyllm import AutoModel
            self._stt_model = AutoModel(model=self._model_role)
        return self._stt_model

    def _build_text_metadata(self, media_path: str, suffix: str) -> dict:
        metadata = {
            'start_time': 0,
            'end_time': -1,
            'audio_file_path': media_path,
            'multimodal_type': 'video_audio_text',
        }
        if suffix == '.mp4':
            metadata['video_file_path'] = media_path
        return metadata

    def _transcribe_text_nodes(
        self, media_path: str, suffix: str, fs: Optional['fsspec.AbstractFileSystem'],
    ) -> List[DocNode]:
        stt_model = self._get_stt_model()
        if stt_model is not None:
            transcript = str(stt_model(media_path)).strip()
            if not transcript:
                return []
            return [DocNode(text=transcript, metadata=self._build_text_metadata(media_path, suffix))]

        try:
            return self._audio_reader._load_data(Path(media_path), fs=fs)
        except ImportError as exc:
            LOG.warning(f'[VideoReader] audio transcription skipped (missing dependency): {exc}')
        except Exception as exc:
            LOG.warning(f'[VideoReader] audio transcription skipped: {exc}')
        return []

    def _safe_name(self, value: str) -> str:
        normalized = ''.join(c if c.isalnum() or c in ('-', '_') else '_' for c in value.strip())
        return normalized or 'video'

    def _format_timestamp(self, seconds: float) -> str:
        total_milliseconds = int(round(seconds * 1000))
        hours, remainder = divmod(total_milliseconds, 3600000)
        minutes, remainder = divmod(remainder, 60000)
        secs, milliseconds = divmod(remainder, 1000)
        return f'{hours:02d}:{minutes:02d}:{secs:02d}.{milliseconds:03d}'

    def _format_timestamp_for_filename(self, seconds: float) -> str:
        return self._format_timestamp(seconds).replace(':', '-').replace('.', '_')

    def _get_frame_dir(self, video_path: str) -> Path:
        src = Path(video_path).resolve()
        FRAME_DIR.mkdir(parents=True, exist_ok=True)
        frame_dir = FRAME_DIR / self._safe_name(src.stem)
        frame_dir.mkdir(parents=True, exist_ok=True)
        return frame_dir

    def _frame_filename(self, video_path: str, index: int) -> str:
        video_name = self._safe_name(Path(video_path).stem)
        timestamp = self._format_timestamp_for_filename(index * self._frame_interval)
        return f'{video_name}_frame_{timestamp}.jpg'

    def _get_video_duration(self, video_path: str) -> float:
        ffprobe_path = shutil.which('ffprobe')
        if not ffprobe_path:
            raise RuntimeError('`ffprobe` not found in PATH.')

        cmd = [
            ffprobe_path,
            '-v', 'error',
            '-show_entries', 'format=duration',
            '-of', 'default=noprint_wrappers=1:nokey=1',
            video_path,
        ]

        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            check=True,
        )

        return float(result.stdout.strip())

    def _extract_frames(self, video_path: str) -> List[str]:
        ffmpeg_path = shutil.which('ffmpeg')
        if not ffmpeg_path:
            raise RuntimeError('`ffmpeg` not found in PATH.')

        frame_dir = self._get_frame_dir(video_path)
        output_pattern = frame_dir / 'raw_%06d.jpg'

        for existing_path in frame_dir.glob('*.jpg'):
            existing_path.unlink()

        duration = self._get_video_duration(video_path)

        if duration < self._frame_interval:
            first_frame_path = frame_dir / 'raw_000001.jpg'

            cmd = [
                ffmpeg_path,
                '-y',
                '-i',
                video_path,
                '-frames:v', '1',
                str(first_frame_path),
            ]

            subprocess.run(
                cmd,
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )

        else:
            cmd = [
                ffmpeg_path,
                '-y',
                '-i',
                video_path,
                '-vf',
                f'fps=1/{self._frame_interval}',
                str(output_pattern),
            ]

            subprocess.run(
                cmd,
                check=True,
                stdout=subprocess.DEVNULL,
                stderr=subprocess.DEVNULL,
            )

        raw_frame_paths = sorted(frame_dir.glob('raw_*.jpg'))

        if not raw_frame_paths:
            raise ValueError(f'No frames extracted from video: {video_path}')

        frame_paths = []

        for idx, raw_frame_path in enumerate(raw_frame_paths):
            readable_path = frame_dir / self._frame_filename(video_path, idx)

            if readable_path.exists():
                readable_path.unlink()

            raw_frame_path.rename(readable_path)
            frame_paths.append(str(readable_path))

        return frame_paths

    def _load_frame_nodes(self, video_path: str, fs: Optional['fsspec.AbstractFileSystem']) -> List[ImageDocNode]:
        frame_paths = self._extract_frames(video_path)
        nodes: List[ImageDocNode] = []
        for idx, frame_path in enumerate(frame_paths):
            frame_nodes = self._image_reader._load_data(Path(frame_path), fs=fs)
            frame_time_seconds = idx * self._frame_interval
            for node in frame_nodes:
                node.metadata['video_source_path'] = video_path
                node.metadata['frame_path'] = frame_path
                node.metadata['frame_index'] = idx
                node.metadata['frame_interval_seconds'] = self._frame_interval
                node.metadata['frame_time_seconds'] = frame_time_seconds
                node.metadata['frame_timestamp'] = self._format_timestamp(frame_time_seconds)
                node.metadata['multimodal_type'] = 'video_audio_frame'
            nodes.extend(frame_nodes)
        return nodes

    def _load_data(
        self, file: Path,
        fs: Optional['fsspec.AbstractFileSystem'] = None,
    ) -> List[DocNode]:
        if not isinstance(file, Path):
            file = Path(file)

        fs = fs or get_default_fs()
        if not is_default_fs(fs):
            raise NotImplementedError('VideoReader currently supports local video paths only')

        video_path = os.path.abspath(str(file))
        suffix = file.suffix.lower()
        text_nodes = self._transcribe_text_nodes(video_path, suffix, fs)

        if suffix == '.mp3':
            return list(text_nodes)
        if suffix == '.mp4':
            frame_nodes = self._load_frame_nodes(video_path, fs=fs)
            out: List[DocNode] = []
            out.extend(text_nodes)
            out.extend(frame_nodes)
            return out
        raise ValueError(f'VideoReader supports .mp3 and .mp4 only, got: {suffix!r}')


__all__ = ['VideoReader']
