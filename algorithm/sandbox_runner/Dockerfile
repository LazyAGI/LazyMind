ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
ARG SANDBOX_PYTHON_TAG=3.11.11-slim-bookworm
FROM ${DOCKER_MIRROR}python:${SANDBOX_PYTHON_TAG}

ARG APT_MIRROR=http://mirrors.aliyun.com/debian
ARG APT_SECURITY_MIRROR=http://mirrors.aliyun.com/debian-security

RUN set -eu; \
    for f in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources; do \
      [ -f "$f" ] || continue; \
      sed -i \
        -e "s|http://deb.debian.org/debian-security|${APT_SECURITY_MIRROR}|g" \
        -e "s|http://deb.debian.org/debian|${APT_MIRROR}|g" \
        "$f"; \
    done; \
    apt-get update; \
    apt-get install -y --no-install-recommends \
      bash ca-certificates coreutils curl diffutils ffmpeg findutils gawk git grep jq libseccomp2 \
      nodejs npm pandoc ripgrep sed tar unzip xz-utils zip; \
    rm -rf /var/lib/apt/lists/*; \
    rm -f \
      /usr/bin/apt /usr/bin/apt-cache /usr/bin/apt-get \
      /usr/bin/dpkg /usr/bin/dpkg-deb /usr/bin/mount /usr/bin/umount \
      /usr/bin/scp /usr/bin/sftp /usr/bin/ssh /usr/bin/su; \
    chmod 600 /etc/passwd /etc/group

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PYTHONPATH=/app

WORKDIR /app
COPY algorithm/lazymind/sandbox_runner /app/sandbox_runner

ENTRYPOINT ["python", "-m", "sandbox_runner.server"]
CMD ["--socket", "/run/lazymind-sandbox/runner.sock", "--root", "/tmp/lazymind-sandbox/sessions"]
