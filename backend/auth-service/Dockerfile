ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
FROM ${DOCKER_MIRROR}python:3.11-slim

ARG PIP_INDEX_URL=https://mirrors.aliyun.com/pypi/simple
ARG PIP_DEFAULT_TIMEOUT=2400
ARG PIP_RETRIES=10

ENV PIP_INDEX_URL=${PIP_INDEX_URL} \
    PIP_DEFAULT_TIMEOUT=${PIP_DEFAULT_TIMEOUT} \
    PIP_RETRIES=${PIP_RETRIES} \
    PIP_DISABLE_PIP_VERSION_CHECK=1

WORKDIR /app

COPY auth-service/requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

COPY auth-service/ .
COPY scripts /app/scripts
COPY core /app/core
RUN python3 /app/scripts/extract_api_permissions.py --output /app/api_permissions.json --exclude scripts,core,vendor /app/core /app

CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
