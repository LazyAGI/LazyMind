ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
FROM ${DOCKER_MIRROR}python:3.11-slim
ARG TARGETARCH

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
RUN python3 /app/scripts/install_github_cli.py --release /app/github-cli-release.json \
    --arch "${TARGETARCH}" --destination /opt/lazymind/github-cli
ENV LAZYMIND_GITHUB_CLI_PATH=/opt/lazymind/github-cli/bin/gh
COPY core /app/core
COPY channel-gateway /channel-gateway

RUN python3 /app/scripts/extract_api_permissions.py \
    --output /app/api_permissions.json \
    --exclude scripts,core,vendor \
    /app/core /app /channel-gateway

CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "8000"]
