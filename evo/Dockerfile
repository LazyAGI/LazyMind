ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
FROM ${DOCKER_MIRROR}python:3.11-slim

ARG OPENCODE_VERSION=1.14.28
ARG LAZYLLM_GITHUB_REF=main
ARG LAZYLLM_ARCHIVE_BASE=https://codeload.github.com/LazyAGI/LazyLLM/tar.gz/refs/heads
ARG APT_MIRROR=http://mirrors.aliyun.com/debian
ARG APT_SECURITY_MIRROR=http://mirrors.aliyun.com/debian-security
ARG PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple
ARG PIP_DEFAULT_TIMEOUT=2400
ARG PIP_RETRIES=10
ARG GITHUB_PROXY=https://gh-proxy.com/

ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    PIP_INDEX_URL=${PIP_INDEX_URL} \
    PIP_DEFAULT_TIMEOUT=${PIP_DEFAULT_TIMEOUT} \
    PIP_RETRIES=${PIP_RETRIES} \
    PIP_DISABLE_PIP_VERSION_CHECK=1 \
    LAZYMIND_EVO_CODE_BINARY=/usr/local/bin/opencode \
    LAZYMIND_EVO_CODE_DATA_DIR=/var/lib/lazymind/evo/opencode \
    LAZYMIND_EVO_BASE_DIR=/var/lib/lazymind/evo \
    LAZYMIND_EVO_CHAT_SOURCE=/app/algorithm/chat \
    LAZYMIND_MODEL_CONFIG_PATH=dynamic \
    PYTHONPATH=/opt/lazyllm:/app

RUN set -eu; \
    for f in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources; do \
      [ -f "$f" ] || continue; \
      sed -i \
        -e "s|http://deb.debian.org/debian-security|${APT_SECURITY_MIRROR}|g" \
        -e "s|http://deb.debian.org/debian|${APT_MIRROR}|g" \
        "$f"; \
    done; \
    apt-get update && apt-get install -y --no-install-recommends \
        git curl ca-certificates ripgrep build-essential cmake ninja-build \
        libgl1 libglib2.0-0 \
    && rm -rf /var/lib/apt/lists/*

# Keep LazyLLM on the official main branch while avoiding the C++ editable build path.
RUN set -eu; \
    curl -fL --http1.1 --no-progress-meter \
      --retry 8 --retry-all-errors --retry-delay 3 \
      --connect-timeout 30 --speed-limit 1024 --speed-time 60 --max-time 900 \
      "${LAZYLLM_ARCHIVE_BASE}/${LAZYLLM_GITHUB_REF}" -o /tmp/lazyllm.tar.gz; \
    mkdir -p /opt/lazyllm; \
    tar -xzf /tmp/lazyllm.tar.gz -C /opt/lazyllm --strip-components=1; \
    rm /tmp/lazyllm.tar.gz; \
    pip install --no-cache-dir \
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

RUN cd /opt/lazyllm && lazyllm install rag

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
RUN pip install -r /tmp/evo-requirements.txt \
    && pip install --no-cache-dir python-dotenv

WORKDIR /app
COPY evo/ /app/evo/
COPY algorithm /app/algorithm
RUN chmod +x /app/evo/service/entrypoint.sh

EXPOSE 8047
ENTRYPOINT ["/app/evo/service/entrypoint.sh"]
