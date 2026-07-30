import lazyllm

from .lazyllm_docs import ensure_lazyllm_docs

ensure_lazyllm_docs(lazyllm)

from .config import config  # noqa: E402

__all__ = ['config']
