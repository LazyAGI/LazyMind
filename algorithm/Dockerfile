# Global ARGs used in FROM directives MUST be declared before the first FROM,
# otherwise the legacy builder (DOCKER_BUILDKIT=0) treats them as stage-scoped
# and leaves them unsubstituted ("base name (${...}) should not be blank").
ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
ARG BASE_LAZYLLM_IMAGE=base_env
ARG BASE_LAZYMIND_IMAGE=base_code
ARG LAZYLLM_VERSION=1.2.2
ARG LAZYLLM_BUNDLE_SOURCE=1

# Stage: base_env - pure environment, NO business code copied.
# Rebuilt only when upgrading lazyllm version or changing requirements.txt.
FROM ${DOCKER_MIRROR}python:3.11-slim AS base_env
WORKDIR /app
ARG LAZYLLM_VERSION

ARG APT_MIRROR=http://mirrors.aliyun.com/debian
ARG APT_SECURITY_MIRROR=http://mirrors.aliyun.com/debian-security
ARG PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple
ARG PIP_DEFAULT_TIMEOUT=2400
ARG PIP_RETRIES=10
ENV PIP_INDEX_URL=${PIP_INDEX_URL} \
    PIP_DEFAULT_TIMEOUT=${PIP_DEFAULT_TIMEOUT} \
    PIP_RETRIES=${PIP_RETRIES} \
    PIP_DISABLE_PIP_VERSION_CHECK=1

# Keep this layer limited to packages required to build/install LazyLLM.
# Do not add optional runtime tools, fonts, or other extensions here: changing
# this layer invalidates the expensive LazyLLM dependency cache below.
RUN set -eu; \
    for f in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources; do \
      [ -f "$f" ] || continue; \
      sed -i \
        -e "s|http://deb.debian.org/debian-security|${APT_SECURITY_MIRROR}|g" \
        -e "s|http://deb.debian.org/debian|${APT_MIRROR}|g" \
        "$f"; \
    done; \
    apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates git build-essential gcc python3-dev \
    && update-ca-certificates \
    && rm -rf /var/lib/apt/lists/*

# Release images always use the pinned PyPI package. Keeping LazyLLM out of
# PYTHONPATH avoids scanning its source checkout and docs during each import.
RUN pip install --no-cache-dir "lazyllm==${LAZYLLM_VERSION}" \
    && python -c "import importlib.metadata as m; assert m.version('lazyllm') == '${LAZYLLM_VERSION}'" \
    && lazyllm install rag

# Optional runtime tools and extensions belong after LazyLLM so changes here do
# not invalidate its large dependency layer.
RUN apt-get update && apt-get install -y --no-install-recommends \
        curl ffmpeg fonts-noto-cjk fonts-wqy-zenhei \
        libgl1 libglib2.0-0 libsm6 libxext6 libxrender1 libxcb1 ripgrep \
    && rm -rf /var/lib/apt/lists/*

# Copy only the dependency manifest so this layer is invalidated only when
# requirements change, not when LazyMind source code changes.
COPY algorithm/requirements.txt algorithm/requirements-cloud.txt /app/algorithm/
RUN pip install --no-cache-dir \
        -r algorithm/requirements.txt \
        -r algorithm/requirements-cloud.txt

# Pre-warm tiktoken encoding cache so the worker never needs to fetch it at
# runtime (openaipublic.blob.core.windows.net may be unreachable in air-gapped
# or restricted environments).
RUN python -c "\
import os, pathlib; \
cache = pathlib.Path('/root/.lazyllm/tiktoken'); \
cache.mkdir(parents=True, exist_ok=True); \
os.environ['TIKTOKEN_CACHE_DIR'] = str(cache); \
import tiktoken; tiktoken.get_encoding('gpt2'); \
print('tiktoken gpt2 cache warmed at', cache)"


# Stage: base_code - copy all source code on top of the fully-installed env.
# Only this stage and its children need to rebuild when code changes.
# NOTE: must use a hard-coded stage name here; Docker does not resolve
# same-file stage references when the name comes from an ARG.
FROM base_env AS base_code
ARG LAZYMIND_GIT_SHA=unknown
ARG LAZYLLM_GIT_SHA=unknown
LABEL org.opencontainers.image.title="LazyMind algorithm/chat"
LABEL org.opencontainers.image.revision="${LAZYMIND_GIT_SHA}"
LABEL io.lazymind.lazymind_git_sha="${LAZYMIND_GIT_SHA}"
LABEL io.lazymind.lazyllm_git_sha="${LAZYLLM_GIT_SHA}"

ARG LAZYLLM_BUNDLE_SOURCE
COPY algorithm/ /app/algorithm/
RUN if [ "${LAZYLLM_BUNDLE_SOURCE}" = "1" ]; then \
      test -d /app/algorithm/lazyllm/lazyllm; \
      mv /app/algorithm/lazyllm /opt/lazyllm; \
    else \
      rm -rf /app/algorithm/lazyllm; \
    fi


# Stage: algorithm - env + code + runtime config. Used by most services.
FROM base_code AS algorithm
ARG LAZYMIND_PRELOAD_WHISPER_MODEL=0
ARG LAZYMIND_WHISPER_MODEL=base

RUN if [ "$LAZYMIND_PRELOAD_WHISPER_MODEL" = "1" ]; then \
        python -c "import whisper; whisper.load_model('${LAZYMIND_WHISPER_MODEL}')"; \
    fi

ENV PYTHONPATH=/opt/lazyllm:/app/algorithm \
  LAZYMIND_DOCUMENT_SERVER_URL=http://parsing:8000 \
  LAZYMIND_SHARED_UPLOAD_DIR=/var/lib/lazymind/uploads \
  LAZYMIND_MAX_CONCURRENCY=10 \
  LAZYMIND_LLM_PRIORITY=0 \
  LAZYMIND_DEFAULT_CHAT_DATASET=algo \
  LAZYMIND_CORE_API_URL=http://core:8000 \
  LAZYMIND_MAX_RETRIES=20 \
  LAZYMIND_AGENTIC_MAX_ROUNDS_LOW=6 \
  LAZYMIND_AGENTIC_MAX_ROUNDS_MEDIUM=20 \
  LAZYMIND_AGENTIC_MAX_ROUNDS_HIGH=60 \
  LAZYMIND_REVIEW_MAX_RETRIES=5 \
  LAZYMIND_SKILL_REVIEW_DEBUG=false \
  LAZYMIND_USE_INNER_MODEL=true \
  LAZYMIND_MODEL_CONFIG_PATH=dynamic
WORKDIR /app/algorithm
CMD ["python", "parsing/parsing.py"]


# Stage: doc - algorithm + doc server code.
FROM algorithm AS doc
WORKDIR /app
ENV PYTHONPATH=/opt/lazyllm:/app/algorithm
COPY backend/core/doc /app/backend/core/doc
CMD ["python", "backend/core/doc/doc_server.py"]


# Stage: opencode_download - fetch the opencode binary in an isolated layer.
# This stage only depends on OPENCODE_VERSION and GITHUB_PROXY, so the binary
# is cached independently of any LazyMind / LazyLLM source changes.
FROM ${DOCKER_MIRROR}python:3.11-slim AS opencode_download
ARG GITHUB_PROXY=""
ARG OPENCODE_VERSION=1.14.28
RUN set -eu; \
    apt-get update && apt-get install -y --no-install-recommends curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*; \
    arch="$(dpkg --print-architecture)"; \
    case "$arch" in \
      amd64) ARCH=x64 ;; \
      arm64) ARCH=arm64 ;; \
      *) echo "unsupported arch $arch" && exit 1 ;; \
    esac; \
    curl -fL "${GITHUB_PROXY}https://github.com/sst/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${ARCH}.tar.gz" \
      -o /tmp/opencode.tar.gz; \
    tar -xzf /tmp/opencode.tar.gz -C /usr/local/bin opencode; \
    chmod 0755 /usr/local/bin/opencode; \
    rm /tmp/opencode.tar.gz; \
    opencode --version


# Stage: evo - algorithm + opencode binary + evo service.
FROM algorithm AS evo
WORKDIR /app
ARG APT_MIRROR
ARG APT_SECURITY_MIRROR

ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    LAZYMIND_EVO_CODE_BINARY=/usr/local/bin/opencode \
    LAZYMIND_EVO_CODE_DATA_DIR=/var/lib/lazymind/evo/opencode \
    LAZYMIND_EVO_BASE_DIR=/var/lib/lazymind/evo \
    LAZYMIND_EVO_CHAT_SOURCE=/app/algorithm/chat \
    LAZYMIND_MODEL_CONFIG_PATH=dynamic \
    PYTHONPATH=/opt/lazyllm:/app/algorithm

RUN set -eu; \
for f in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources; do \
  [ -f "$f" ] || continue; \
  sed -i \
    -e "s|http://deb.debian.org/debian-security|${APT_SECURITY_MIRROR}|g" \
    -e "s|http://deb.debian.org/debian|${APT_MIRROR}|g" \
    "$f"; \
done; \
apt-get update && apt-get install -y --no-install-recommends \
    ripgrep cmake ninja-build git \
&& rm -rf /var/lib/apt/lists/*

# Copy the pre-built opencode binary from the isolated download stage.
COPY --from=opencode_download /usr/local/bin/opencode /usr/local/bin/opencode

COPY evo/requirements.txt /tmp/evo-requirements.txt

RUN pip install --no-cache-dir -r /tmp/evo-requirements.txt python-dotenv

COPY evo/ /app/evo/

EXPOSE 8047
ENTRYPOINT ["python", "-m", "evo.app"]


# Stage: mineru - base_env (full env, no code) + torch/mineru extras + code.
# Inherits from base_env so torch/mineru layers are cached independently of
# lazyllm/LazyMind source changes.
# NOTE: hard-coded stage name; ARG-based FROM does not resolve in-file stages.
FROM base_env AS mineru
ARG LAZYMIND_GIT_SHA=unknown
ARG LAZYLLM_GIT_SHA=unknown
LABEL org.opencontainers.image.title="LazyMind algorithm/mineru"
LABEL org.opencontainers.image.revision="${LAZYMIND_GIT_SHA}"
LABEL io.lazymind.lazymind_git_sha="${LAZYMIND_GIT_SHA}"
LABEL io.lazymind.lazyllm_git_sha="${LAZYLLM_GIT_SHA}"
ARG MINERU_VERSION=2.7.1
ARG MINERU_PACKAGE_VARIANT=pipeline
ARG MINERU_PREINSTALL_CPU_TORCH=1
ARG MINERU_TORCH_VERSION=2.11.0
ARG MINERU_TORCHVISION_VERSION=0.26.0
ARG MINERU_NUMPY_VERSION=1.26.4
ARG MINERU_PYTORCH_INDEX_URL=https://download.pytorch.org/whl/cpu
RUN if [ "$MINERU_PREINSTALL_CPU_TORCH" = "1" ]; then \
        pip install --no-cache-dir \
        --index-url "$MINERU_PYTORCH_INDEX_URL" \
        "torch==${MINERU_TORCH_VERSION}+cpu" \
        "torchvision==${MINERU_TORCHVISION_VERSION}+cpu"; \
    fi
RUN pip install --no-cache-dir "mineru[${MINERU_PACKAGE_VARIANT}]==${MINERU_VERSION}"
RUN if [ -n "$MINERU_NUMPY_VERSION" ]; then \
        pip install --no-cache-dir "numpy==${MINERU_NUMPY_VERSION}"; \
    fi
ARG LAZYLLM_BUNDLE_SOURCE
COPY algorithm/ /app/algorithm/
RUN if [ "${LAZYLLM_BUNDLE_SOURCE}" = "1" ]; then \
      test -d /app/algorithm/lazyllm/lazyllm; \
      mv /app/algorithm/lazyllm /opt/lazyllm; \
    else \
      rm -rf /app/algorithm/lazyllm; \
    fi
ENV PYTHONPATH=/opt/lazyllm:/app/algorithm
WORKDIR /app/algorithm
CMD ["python", "parsing/mineru.py"]
