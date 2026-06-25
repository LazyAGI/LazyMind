ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
FROM ${DOCKER_MIRROR}python:3.11-slim

ARG APT_MIRROR=http://mirrors.aliyun.com/debian
ARG APT_SECURITY_MIRROR=http://mirrors.aliyun.com/debian-security
ARG PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple
ARG PIP_DEFAULT_TIMEOUT=2400
ARG PIP_RETRIES=10

ENV DEBIAN_FRONTEND=noninteractive \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_INDEX_URL=${PIP_INDEX_URL} \
    PIP_DEFAULT_TIMEOUT=${PIP_DEFAULT_TIMEOUT} \
    PIP_RETRIES=${PIP_RETRIES} \
    PIP_DISABLE_PIP_VERSION_CHECK=1

WORKDIR /app

RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \
    --mount=type=cache,target=/var/lib/apt,sharing=locked \
    set -eu; \
    for f in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources; do \
      [ -f "$f" ] || continue; \
      sed -i \
        -e "s|http://deb.debian.org/debian-security|${APT_SECURITY_MIRROR}|g" \
        -e "s|http://deb.debian.org/debian|${APT_MIRROR}|g" \
        "$f"; \
    done; \
    apt-get update \
    && apt-get install -y --no-install-recommends \
        libreoffice libreoffice-l10n-zh-cn default-jre-headless tini \
        curl ca-certificates fontconfig \
        fonts-dejavu-core fonts-noto-cjk fonts-wqy-zenhei fonts-wqy-microhei \
    && fc-cache -f -v

COPY office-convert-service/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY office-convert-service/ .

ENTRYPOINT ["/usr/bin/tini", "--"]
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8080"]
