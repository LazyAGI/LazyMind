# Global ARGs used in FROM directives MUST be declared before the first FROM,
# otherwise the legacy builder (DOCKER_BUILDKIT=0) treats them as stage-scoped
# and leaves them unsubstituted ("base name (${...}) should not be blank").
ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
ARG BASE_LAZYLLM_IMAGE=base
ARG BASE_LAZYMIND_IMAGE=base_lazymind

# Stage: base - lazyllm[rag] only. Rebuilt only when upgrading lazyllm version.
FROM ${DOCKER_MIRROR}python:3.11-slim AS base
WORKDIR /app

ARG APT_MIRROR=http://mirrors.aliyun.com/debian
ARG APT_SECURITY_MIRROR=http://mirrors.aliyun.com/debian-security
ARG PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple
ARG PIP_DEFAULT_TIMEOUT=2400
ARG PIP_RETRIES=10
ENV PIP_INDEX_URL=${PIP_INDEX_URL} \
    PIP_DEFAULT_TIMEOUT=${PIP_DEFAULT_TIMEOUT} \
    PIP_RETRIES=${PIP_RETRIES} \
    PIP_DISABLE_PIP_VERSION_CHECK=1

RUN set -eu; \
    for f in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources; do \
      [ -f "$f" ] || continue; \
      sed -i \
        -e "s|http://deb.debian.org/debian-security|${APT_SECURITY_MIRROR}|g" \
        -e "s|http://deb.debian.org/debian|${APT_MIRROR}|g" \
        "$f"; \
    done; \
    apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl ffmpeg build-essential gcc \
        libgl1 libglib2.0-0 libsm6 libxext6 libxrender1 libxcb1 \
        python3-dev \
    && update-ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY algorithm/lazyllm /opt/lazyllm
RUN pip install --no-cache-dir \
      toml tqdm filelock pyyaml packaging \
      "fastapi>=0.111.0" "loguru>=0.7.2" "pydantic>=2.11.7,<3.0.0" \
      "requests>=2.32.2" "uvicorn>=0.23.2" "cloudpickle>=3.0.0" \
      "docstring-parser>=0.16,<0.17" "json5>=0.9.25,<0.10.0" \
      "pyjwt>=2.8.0" "psutil>=6.0.0,<7.0.0" "deepdiff>=8.6.1"; \
    site_packages="$(python -c 'import sysconfig; print(sysconfig.get_paths()["purelib"])')"; \
    printf '/opt/lazyllm\n' > "${site_packages}/lazyllm-main.pth"; \
    printf '%s\n' '#!/usr/bin/env sh' \
      'PYTHONPATH="/opt/lazyllm:${PYTHONPATH:-}" exec python -m lazyllm.cli.main "$@"' \
      > /usr/local/bin/lazyllm; \
    chmod 0755 /usr/local/bin/lazyllm


# Stage: base_lazymind - base.
FROM ${BASE_LAZYLLM_IMAGE} AS base_lazymind
ARG LAZYMIND_GIT_SHA=unknown
ARG LAZYLLM_GIT_SHA=unknown
LABEL org.opencontainers.image.title="LazyMind algorithm/chat"
LABEL org.opencontainers.image.revision="${LAZYMIND_GIT_SHA}"
LABEL io.lazymind.lazymind_git_sha="${LAZYMIND_GIT_SHA}"
LABEL io.lazymind.lazyllm_git_sha="${LAZYLLM_GIT_SHA}"
RUN cd /opt/lazyllm && lazyllm install rag


# Stage: algorithm - base + requirements.txt + code. Rebuilt when requirements.txt or code changes.
FROM ${BASE_LAZYMIND_IMAGE} AS algorithm
ARG LAZYMIND_PRELOAD_WHISPER_MODEL=0
ARG LAZYMIND_WHISPER_MODEL=base

COPY algorithm/requirements.txt /app/algorithm/requirements.txt
RUN pip install --no-cache-dir -r algorithm/requirements.txt

RUN if [ "$LAZYMIND_PRELOAD_WHISPER_MODEL" = "1" ]; then \
        python -c "import whisper; whisper.load_model('${LAZYMIND_WHISPER_MODEL}')"; \
    fi
COPY algorithm/ /app/algorithm/
RUN rm -rf /app/algorithm/lazyllm
ENV PYTHONPATH=/opt/lazyllm:/app/algorithm \
  LAZYMIND_DOCUMENT_SERVER_URL=http://parsing:8000 \
  LAZYMIND_SHARED_UPLOAD_DIR=/var/lib/lazymind/uploads \
  LAZYMIND_MAX_CONCURRENCY=10 \
  LAZYMIND_LLM_PRIORITY=0 \
  LAZYMIND_DEFAULT_CHAT_DATASET=algo \
  LAZYMIND_CORE_API_URL=http://core:8000 \
  LAZYMIND_MAX_RETRIES=20 \
  LAZYMIND_REVIEW_MAX_RETRIES=5 \
  LAZYMIND_MEMORY_REVIEW_INTERVAL=1 \
  LAZYMIND_SKILL_REVIEW_INTERVAL=5 \
  LAZYMIND_SKILL_REVIEW_DEBUG=false \
  LAZYMIND_USE_INNER_MODEL=true \
  LAZYMIND_MODEL_CONFIG_PATH=dynamic
WORKDIR /app/algorithm
CMD ["python", "parsing/parsing.py"]


# Stage: doc - base + requirements.txt + code. Rebuilt when requirements.txt or code changes.
FROM algorithm AS doc
WORKDIR /app
ENV PYTHONPATH=/opt/lazyllm:/app/algorithm
COPY backend/core/doc /app/backend/core/doc
CMD ["python", "backend/core/doc/doc_server.py"]


# Stage: evo - base + requirements.txt + code. Rebuilt when requirements.txt or code changes.
FROM algorithm AS evo
WORKDIR /app
ARG APT_MIRROR=http://mirrors.aliyun.com/debian
ARG APT_SECURITY_MIRROR=http://mirrors.aliyun.com/debian-security
ARG GITHUB_PROXY=https://gh-proxy.com/
ARG OPENCODE_VERSION=1.14.28

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

# Detect arch inside the container (dpkg) so the binary always matches the base image,
# regardless of whether BuildKit injects TARGETARCH (legacy builder does not).
RUN set -eu; \
  arch="$(dpkg --print-architecture)"; \
  case "$arch" in \
    amd64) ARCH=x64 ;; \
    arm64) ARCH=arm64 ;; \
    *) echo "unsupported arch $arch" && exit 1 ;; \
  esac; \
  curl -fL "${GITHUB_PROXY}https://github.com/sst/opencode/releases/download/v${OPENCODE_VERSION}/opencode-linux-${ARCH}.tar.gz" -o /tmp/opencode.tar.gz; \
  tar -xzf /tmp/opencode.tar.gz -C /usr/local/bin opencode; \
  chmod 0755 /usr/local/bin/opencode; \
  rm /tmp/opencode.tar.gz; \
  opencode --version

COPY evo/requirements.txt /tmp/evo-requirements.txt

RUN pip install --no-cache-dir -r /tmp/evo-requirements.txt python-dotenv

COPY evo/ /app/evo/

RUN chmod +x /app/evo/service/entrypoint.sh

EXPOSE 8047
ENTRYPOINT ["/app/evo/service/entrypoint.sh"]

# Stage: mineru - base + requirements.txt + configurable mineru extra + code.
FROM base AS mineru
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
COPY algorithm/requirements.txt /app/algorithm/requirements.txt
RUN pip install --no-cache-dir -r algorithm/requirements.txt
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
COPY algorithm/ /app/algorithm/
RUN rm -rf /app/algorithm/lazyllm
ENV PYTHONPATH=/opt/lazyllm:/app/algorithm
WORKDIR /app/algorithm
CMD ["python", "parsing/mineru.py"]
