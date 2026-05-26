ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
FROM ${DOCKER_MIRROR}python:3.11-slim

ARG APT_MIRROR=http://mirrors.aliyun.com/debian
ARG APT_SECURITY_MIRROR=http://mirrors.aliyun.com/debian-security
ARG PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple
ARG PIP_DEFAULT_TIMEOUT=2400
ARG PIP_RETRIES=10

ENV PIP_INDEX_URL=${PIP_INDEX_URL} \
    PIP_DEFAULT_TIMEOUT=${PIP_DEFAULT_TIMEOUT} \
    PIP_RETRIES=${PIP_RETRIES} \
    PIP_DISABLE_PIP_VERSION_CHECK=1

WORKDIR /app

RUN set -eu; \
    for f in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources; do \
      [ -f "$f" ] || continue; \
      sed -i \
        -e "s|http://deb.debian.org/debian-security|${APT_SECURITY_MIRROR}|g" \
        -e "s|http://deb.debian.org/debian|${APT_MIRROR}|g" \
        "$f"; \
    done; \
    apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl build-essential gcc python3-dev \
    && update-ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY algorithm/requirements.txt /app/algorithm/requirements.txt
RUN pip install --no-cache-dir 'lazyllm[rag]==0.7.6a0' \
    && pip install --no-cache-dir -r /app/algorithm/requirements.txt

COPY algorithm /app/algorithm
COPY backend/core/doc /app/backend/core/doc

ENV PYTHONPATH=/app/algorithm

CMD ["python", "backend/core/doc/doc_server.py"]
