# Kong with custom rbac-auth plugin (needs lua-resty-http for auth-service calls)
ARG DOCKER_MIRROR=docker.m.daocloud.io/library/
FROM ${DOCKER_MIRROR}kong:3.6

ARG APT_MIRROR=http://mirrors.aliyun.com/debian
ARG APT_SECURITY_MIRROR=http://mirrors.aliyun.com/debian-security
ARG APT_UBUNTU_MIRROR=http://mirrors.aliyun.com/ubuntu
ARG APT_UBUNTU_PORTS_MIRROR=http://mirrors.aliyun.com/ubuntu-ports
ARG GITHUB_PROXY=https://gh-proxy.com/
USER root
RUN set -eu; \
    for f in /etc/apt/sources.list /etc/apt/sources.list.d/*.sources /etc/apt/sources.list.d/*.list; do \
      [ -f "$f" ] || continue; \
      sed -i \
        -e "s|http://ports.ubuntu.com/ubuntu-ports|${APT_UBUNTU_PORTS_MIRROR}|g" \
        -e "s|https://ports.ubuntu.com/ubuntu-ports|${APT_UBUNTU_PORTS_MIRROR}|g" \
        -e "s|http://archive.ubuntu.com/ubuntu|${APT_UBUNTU_MIRROR}|g" \
        -e "s|http://security.ubuntu.com/ubuntu|${APT_UBUNTU_MIRROR}|g" \
        -e "s|http://deb.debian.org/debian-security|${APT_SECURITY_MIRROR}|g" \
        -e "s|http://deb.debian.org/debian|${APT_MIRROR}|g" \
        "$f"; \
    done; \
    apt-get update && apt-get install -y --no-install-recommends git ca-certificates curl \
    && unset http_proxy https_proxy no_proxy HTTP_PROXY HTTPS_PROXY NO_PROXY \
    && downloaded=0; \
    for url in \
      "${GITHUB_PROXY}https://github.com/ledgetech/lua-resty-http/archive/refs/tags/v0.17.1.tar.gz" \
      "https://mirror.ghproxy.com/https://github.com/ledgetech/lua-resty-http/archive/refs/tags/v0.17.1.tar.gz" \
      "https://github.com/ledgetech/lua-resty-http/archive/refs/tags/v0.17.1.tar.gz"; do \
      curl -fL --retry 3 --retry-delay 5 --connect-timeout 30 "$url" -o lua-resty-http-0.17.1.tar.gz && downloaded=1 && break; \
    done; \
    test "$downloaded" -eq 1 \
    && tar -xzf lua-resty-http-0.17.1.tar.gz && cd lua-resty-http-0.17.1 \
    && luarocks make lua-resty-http-0.17.1-0.rockspec && cd .. \
    && apt-get purge -y git curl \
    && apt-get autoremove -y \
    && rm -rf /var/lib/apt/lists/* && rm -rf lua-resty-http-0.17.1.tar.gz && rm -rf lua-resty-http-0.17.1
    
USER kong
COPY plugins/rbac-auth /usr/local/share/lua/5.1/kong/plugins/rbac-auth
ENV KONG_PLUGINS=bundled,rbac-auth
