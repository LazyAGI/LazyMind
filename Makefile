# Code style: Python (flake8) + Go (gofmt). Mirrors algorithm/lazyllm Makefile pattern.
.PHONY: help lint install-flake8 install-golangci-lint lint-python lint-go lint-state-backend-boundary test test-hermetic test-hermetic-setup test-hermetic-check build up up-build up-build-local down clear reset-kb reset-all fresh-start compose-host-permissions file-watcher-dirs file-watcher-build file-watcher-run file-watcher-start file-watcher-stop desktop-stop-if-present
.DEFAULT_GOAL := help

# Use legacy Docker builder by default to avoid pulling moby/buildkit:buildx-stable-1 from Docker Hub
# (which often times out in restricted networks). Override with: make up DOCKER_BUILDKIT=1
export DOCKER_BUILDKIT ?= 1
PYTHON ?= python3
PIP ?= $(PYTHON) -m pip
GO ?= go
LAZYMIND_LOCAL_PROFILE ?= linux-browser
LAZYMIND_LOCAL_BIN ?= local/local-runtime-manager/lazymind-local
LAZYMIND_LOCAL_GOCACHE ?= $(CURDIR)/.codex-gocache/go-build
PROCESS_COMPOSE_BIN ?= local/bin/process-compose
PROCESS_COMPOSE_PKG ?= github.com/f1bonacc1/process-compose@v1.116.0
comma := ,

# ---------------------------------------------------------------------------
# Mirror profile: cn (domestic/default) or intl (international).
# Selects which .env.mirrors.<profile> file to load for all build-time source
# URLs (Docker Hub mirror, PyPI, APT, Alpine, npm, Go proxy, GitHub proxy).
#
# Priority (highest → lowest):
#   1. Command-line:  make up MIRROR_PROFILE=intl
#   2. .env file:     MIRROR_PROFILE=intl  (or any individual VAR=value)
#   3. Profile file:  .env.mirrors.cn / .env.mirrors.intl
#   4. Makefile ?=:   hard-coded domestic fallbacks below
#
# Usage without Makefile (docker compose directly):
#   docker compose --env-file .env.mirrors.intl up -d
# ---------------------------------------------------------------------------
# Read MIRROR_PROFILE from .env via shell before any include, so that setting
# MIRROR_PROFILE=intl in .env correctly selects the intl profile file.
MIRROR_PROFILE ?= $(or $(shell grep -m1 '^MIRROR_PROFILE=' .env 2>/dev/null | cut -d= -f2-),cn)
_MIRROR_ENV_FILE := .env.mirrors.$(MIRROR_PROFILE)
ifneq (,$(wildcard $(_MIRROR_ENV_FILE)))
include $(_MIRROR_ENV_FILE)
export $(shell sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' $(_MIRROR_ENV_FILE))
endif
# Load .env after the profile so individual variable overrides in .env win.
ifneq (,$(wildcard .env))
include .env
export $(shell sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' .env)
endif

# ---------------------------------------------------------------------------
# Compose project (optional). Pass -p only when COMPOSE_PROJECT is set.
# Usage: make up                           →  docker compose up -d
#        make up COMPOSE_PROJECT=myproj    →  docker compose -p myproj up -d
#        make down                         →  docker compose down
#        make down COMPOSE_PROJECT=myproj  →  docker compose -p myproj down
#        make up-build-local               → use local-runtime-manager with local/docker-compose.local.yml
# ---------------------------------------------------------------------------
_COMPOSE_PROJECT_FLAG := $(if $(COMPOSE_PROJECT),-p $(COMPOSE_PROJECT),)
_COMPOSE_DEFAULT := DOCKER_BUILDKIT=$(DOCKER_BUILDKIT) docker compose $(_COMPOSE_PROJECT_FLAG)
_COMPOSE_LOCAL := DOCKER_BUILDKIT=$(DOCKER_BUILDKIT) docker compose -f docker-compose.yml -f local/docker-compose.local.yml $(_COMPOSE_PROJECT_FLAG)
_COMPOSE := DOCKER_BUILDKIT=$(DOCKER_BUILDKIT) docker compose $(_COMPOSE_PROJECT_FLAG)

# ---------------------------------------------------------------------------
# Scan / file-watcher process
# ---------------------------------------------------------------------------
# file-watcher runs in compose by default. Host mode is kept for local
# debugging and disables the compose file-watcher service on make up.
# Keep its writable roots under the compose volume root by default.
# LAZYMIND_FILE_WATCHER_BASE_ROOT is exported as a compose-friendly path;
# internal Makefile bookkeeping uses the resolved absolute path below.
export LAZYMIND_FILE_WATCHER_BASE_ROOT ?= ./data/scan
LAZYMIND_FILE_WATCHER_BASE_ROOT_ABS := $(abspath $(LAZYMIND_FILE_WATCHER_BASE_ROOT))
export LAZYMIND_FILE_WATCHER_MODE ?= container

# Auto-detect host OS for path style and default watch directory.
# Override in .env or on the command line if needed.
ifeq ($(OS),Windows_NT)
  export LAZYMIND_FILE_WATCHER_HOST_PATH_STYLE ?= windows
  export LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR  ?= D:/
else
  _UNAME_S := $(shell uname -s 2>/dev/null)
  _UNAME_R := $(shell uname -r 2>/dev/null | tr '[:upper:]' '[:lower:]')
  ifeq ($(_UNAME_S),Darwin)
    export LAZYMIND_FILE_WATCHER_HOST_PATH_STYLE ?= posix
    export LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR  ?= $(HOME)
  else ifneq (,$(findstring microsoft,$(_UNAME_R))$(findstring wsl,$(_UNAME_R)))
    export LAZYMIND_FILE_WATCHER_HOST_PATH_STYLE ?= posix
    export LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR  ?= /mnt
  else
    export LAZYMIND_FILE_WATCHER_HOST_PATH_STYLE ?= posix
    export LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR  ?= $(HOME)
  endif
endif

_LAZYMIND_FW_WATCH_HOST_DIR_RAW := $(LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR)
_LAZYMIND_FW_WATCH_HOST_DIR_ABS := $(abspath $(_LAZYMIND_FW_WATCH_HOST_DIR_RAW))
override LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR := $(if $(filter windows,$(LAZYMIND_FILE_WATCHER_HOST_PATH_STYLE)),$(_LAZYMIND_FW_WATCH_HOST_DIR_RAW),$(_LAZYMIND_FW_WATCH_HOST_DIR_ABS))
export SCAN_CONTROL_PLANE_LOCAL_FS_PUBLIC_ROOT := $(LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR)
LAZYMIND_FILE_WATCHER_DIR := backend/file-watcher
LAZYMIND_FILE_WATCHER_BIN := $(LAZYMIND_FILE_WATCHER_DIR)/file_watcher
LAZYMIND_FILE_WATCHER_CONFIG := $(LAZYMIND_FILE_WATCHER_DIR)/configs/agent.yaml
LAZYMIND_FILE_WATCHER_PID := $(LAZYMIND_FILE_WATCHER_BASE_ROOT_ABS)/run/file_watcher.pid
LAZYMIND_FILE_WATCHER_CONSOLE_LOG := $(LAZYMIND_FILE_WATCHER_BASE_ROOT_ABS)/logs/file_watcher.console.log

# ---------------------------------------------------------------------------
# Environment variables (override via: make up VAR=value, or set in .env)
# Only variables that users are likely to change are listed here.
# Internal service URLs, version pins, and fixed paths are hardcoded in docker-compose.yml.
# ---------------------------------------------------------------------------

# Auth — credentials and secrets (change in production)
export LAZYMIND_DATABASE_URL ?= postgresql+psycopg://app:app@db:5432/app
export LAZYMIND_JWT_SECRET ?= dev-secret-change-me
export LAZYMIND_BOOTSTRAP_ADMIN_USERNAME ?= admin
export LAZYMIND_BOOTSTRAP_ADMIN_PASSWORD ?= admin
export LAZYMIND_RESET_ALGO_ON_STARTUP ?= false
export LAZYMIND_RESET_ALL_ON_STARTUP ?= false
export LAZYLLM_ALGO_REGISTER_POLICY ?= none

# Core database
export LAZYMIND_CORE_DATABASE_URL ?= postgresql+psycopg://root:123456@db:5432/core

# OCR routing is selected per-request via the model provider UI (DynamicPDFReader).
# Use LAZYMIND_DEPLOY_MINERU to deploy built-in MinerU profile.
# PaddleOCR compose profile is temporarily disabled (needs GPU).
export LAZYMIND_DEPLOY_MINERU ?= 0
# export LAZYMIND_DEPLOY_PADDLEOCR ?= 0
# Vector / segment stores — override to use external services (skips built-in profile)
export LAZYMIND_MILVUS_URI ?= http://milvus:19530
export LAZYMIND_OPENSEARCH_URI ?= https://opensearch:9200
export LAZYMIND_OPENSEARCH_USER ?= admin
export LAZYMIND_OPENSEARCH_PASSWORD ?= LazyRAG_OpenSearch123!

# Dashboard toggles (set to 1 to enable Attu / OpenSearch Dashboards)
export LAZYMIND_ENABLE_STORE_DASHBOARDS ?= 0
export LAZYMIND_ENABLE_MILVUS_DASHBOARD ?= $(LAZYMIND_ENABLE_STORE_DASHBOARDS)
export LAZYMIND_ENABLE_OPENSEARCH_DASHBOARD ?= $(LAZYMIND_ENABLE_STORE_DASHBOARDS)

# Chat tuning
export LAZYMIND_MAX_CONCURRENCY ?= 10
export LAZYMIND_LLM_PRIORITY ?= 0
export LAZYMIND_ENABLE_ROUTER ?= true

# Tracing (set LAZYLLM_TRACE_ENABLED=0 to disable; requires LANGFUSE_* keys when enabled)
export LAZYLLM_TRACE_ENABLED ?= 1
export LAZYLLM_TRACE_BACKEND ?= local

# MinIO credentials (used by built-in Milvus profile)
export MINIO_ACCESS_KEY ?= minioadmin
export MINIO_SECRET_KEY ?= minioadmin

# Pluggable parent images for the algorithm Dockerfile's multi-stage chain:
#
#   FROM ${BASE_LAZYLLM_IMAGE}  AS mineru     # base_env: apt + lazyllm[rag] + requirements, no code
#   FROM ${BASE_LAZYMIND_IMAGE} AS base_code  # base_code: base_env + COPY lazyllm + algorithm code
#
# Defaults wire up the in-tree chain: base_env -> base_code -> algorithm.
# Override with an external prebuilt image tag to skip heavy build stages
# (useful for CI cache reuse), e.g.:
#   BASE_LAZYMIND_IMAGE=registry.example.com/lazymind/base_code:latest
export BASE_LAZYLLM_IMAGE ?= base_env
export BASE_LAZYMIND_IMAGE ?= base_code
# export BASE_LAZYMIND_IMAGE ?= registry.cn-sh-01.sensecore.cn/ai-expert-service/lazymind-base:2026.05.15.beta

# model config path
export LAZYMIND_MODEL_CONFIG_PATH ?= dynamic

# Frontend port (default 8090; override if the port is occupied, e.g. by Cursor)
export LAZYMIND_FRONTEND_PORT ?= 8090

# Python dirs to lint (exclude submodule algorithm/lazyllm via .flake8)
PYTHON_DIRS := algorithm backend evo

# Go dirs to lint
GO_DIRS := backend/core local/local-proxy
GO_MODULE_DIRS := backend/core backend/scan-control-plane backend/file-watcher local/local-proxy local/local-runtime-manager tests/backend/core
GOLANGCI_LINT_VERSION ?= v2.12.2
GOLANGCI_LINT ?= $(shell command -v golangci-lint 2>/dev/null || printf '%s/bin/golangci-lint' "$$($(GO) env GOPATH)")

help:
	@echo "LazyMind Make targets:"
	@echo "  make up         - Start services in background (with derived profiles)"
	@echo "                    file-watcher runs in compose by default"
	@echo "                    Use LAZYMIND_FILE_WATCHER_MODE=host for host-process debugging"
	@echo "                    Use SERVICES=svc1,svc2 to start specific services only"
	@echo "  make up-build   - Build images and start services"
	@echo "                    Use SERVICES=svc1,svc2 to target specific services"
	@echo "  make up-build-local - Build/start local LazyMind through local-runtime-manager"
	@echo "  make down       - Stop services"
	@echo "                    Tries Cloud/Kong, Local Runtime, and optional Desktop stop paths"
	@echo "                    Use SERVICES=svc1,svc2 to stop specific services only"
	@echo "  make build      - Build compose services (mineru profile only when needed)"
	@echo "                    Use SERVICES=svc1,svc2 to build specific services"
	@echo "                    Use LAZYMIND_ENABLE_STORE_DASHBOARDS=1 to add Attu/OpenSearch Dashboards for built-in stores"
	@echo "  make file-watcher-start - Rebuild and start host file-watcher"
	@echo "  make file-watcher-stop  - Stop host file-watcher started by Makefile"
	@echo "  make lint       - Run Python flake8 and Go gofmt checks"
	@echo "  make test       - Run project test script"
	@echo "  make test-hermetic - Prepare an isolated host test env and run the same scope as make test"
	@echo "  make test-hermetic-setup - Prepare the uv-managed Python test env and check Node/Go"
	@echo "  make test-hermetic-check - Check uv, fnm/nvm, Node 20, Go 1.24.0, and the test venv"
	@echo "  make clear      - Stop services, remove volumes, clear Python cache"
	@echo "  make reset-kb   - Stop services, wipe KB data (Milvus, OpenSearch, uploads, lazyllm DB tables)"
	@echo "                    Set LAZYMIND_RESET_ALGO_ON_STARTUP=true to also clear algo state on next startup"
	@echo "  make reset-all  - Stop services, wipe ALL persistent data (KB + users, auth, Redis, etc.)"
	@echo "                    Equivalent to a clean first-run state"
	@echo "  make fresh-start - reset-kb + up with LAZYMIND_RESET_ALGO_ON_STARTUP=true (standard clean restart)"
	@echo ""
	@echo "Mirror profile (build-time source URLs):"
	@echo "  make up MIRROR_PROFILE=cn    - Use domestic mirrors (default: Aliyun/goproxy.cn/daocloud)"
	@echo "  make up MIRROR_PROFILE=intl  - Use international mirrors (Docker Hub/PyPI/golang.org)"
	@echo "  Set MIRROR_PROFILE=intl in .env for a persistent override."
	@echo "  Without Makefile: docker compose --env-file .env.mirrors.intl up -d"

# Require flake8 to be installed (e.g. in a venv). Do not auto pip-install to avoid PEP 668 errors.
install-flake8:
	@for pkg in flake8 flake8-quotes flake8-bugbear flake8-tidy-imports; do \
		case $$pkg in \
			flake8) mod="flake8" ;; \
			flake8-quotes) mod="flake8_quotes" ;; \
			flake8-bugbear) mod="bugbear" ;; \
			flake8-tidy-imports) mod="flake8_tidy_imports" ;; \
		esac; \
		$(PYTHON) -c "import importlib.util, sys; sys.exit(0 if importlib.util.find_spec('$$mod') else 1)" \
			|| $(PIP) install $$pkg; \
	done

install-golangci-lint:
	@if [ ! -x "$(GOLANGCI_LINT)" ]; then \
		echo "Installing golangci-lint $(GOLANGCI_LINT_VERSION) to $(GOLANGCI_LINT)..."; \
		mkdir -p "$$(dirname "$(GOLANGCI_LINT)")"; \
		GOBIN="$$(dirname "$(GOLANGCI_LINT)")" $(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION); \
	fi

lint-python: install-flake8
	@echo "🐍 Linting Python ($(PYTHON_DIRS))..."
	@$(PYTHON) -m flake8 $(PYTHON_DIRS)

lint-go:
	@echo "🔷 Linting Go ($(GO_DIRS))..."
	@FMT=$$(gofmt -l -s $(GO_DIRS) 2>/dev/null); \
	if [ -n "$$FMT" ]; then \
		echo "❌ Go files not formatted (run: gofmt -w -s $(GO_DIRS)):"; \
		echo "$$FMT"; \
		exit 1; \
	fi
	@echo "✅ Go fmt OK."

lint-state-backend-boundary: install-golangci-lint
	@echo "🔒 Checking state backend Redis boundary..."
	@set -e; for dir in $(GO_MODULE_DIRS); do \
		if [ -f "$$dir/go.mod" ]; then \
			(cd "$$dir" && "$(GOLANGCI_LINT)" run --config "$(CURDIR)/.golangci.yml" --enable-only depguard --tests=false ./...); \
		fi; \
	done
	@echo "✅ State backend boundary OK."

lint: lint-python lint-go lint-state-backend-boundary

test:
	@./tests/run-all.sh

test-hermetic-setup:
	@./tests/test-hermetic-env.sh setup

test-hermetic-check:
	@./tests/test-hermetic-env.sh check

test-hermetic:
	@./tests/test-hermetic-run.sh

# Only mineru has build:; paddleocr/milvus/opensearch use image: only, so only needed for up.
_need_mineru := $(filter 1 true TRUE yes YES on ON,$(LAZYMIND_DEPLOY_MINERU))
# _need_paddleocr := $(filter 1 true TRUE yes YES on ON,$(LAZYMIND_DEPLOY_PADDLEOCR))  # needs GPU
# Deploy milvus/opensearch only when URI exactly matches the built-in services; external URIs = no deployment
_builtin_milvus_uris := http://milvus:19530 http://milvus:19530/
_builtin_opensearch_uris := https://opensearch:9200 https://opensearch:9200/
_need_milvus := $(filter $(strip $(LAZYMIND_MILVUS_URI)),$(_builtin_milvus_uris))
_need_opensearch := $(filter $(strip $(LAZYMIND_OPENSEARCH_URI)),$(_builtin_opensearch_uris))
_enable_milvus_dashboard := $(filter 1 true TRUE yes YES on ON,$(LAZYMIND_ENABLE_MILVUS_DASHBOARD))
_enable_opensearch_dashboard := $(filter 1 true TRUE yes YES on ON,$(LAZYMIND_ENABLE_OPENSEARCH_DASHBOARD))
_need_milvus_dashboard := $(and $(_need_milvus),$(_enable_milvus_dashboard))
_need_opensearch_dashboard := $(and $(_need_opensearch),$(_enable_opensearch_dashboard))

# Start/build profile flags are mode-aware. Cleanup profile flags are intentionally exhaustive.
_COMPOSE_PROFILES := $(strip $(if $(_need_mineru),--profile mineru) $(if $(_need_milvus),--profile milvus) $(if $(_need_opensearch),--profile opensearch) $(if $(_need_milvus_dashboard),--profile milvus-dashboard) $(if $(_need_opensearch_dashboard),--profile opensearch-dashboard))
_CLEANUP_COMPOSE_PROFILES := --profile mineru --profile paddleocr --profile milvus --profile opensearch --profile milvus-dashboard --profile opensearch-dashboard --profile file-watcher-artifact
_COMPOSE_FILE_WATCHER_SCALE := $(if $(filter container,$(LAZYMIND_FILE_WATCHER_MODE)),,--scale file-watcher=0)
_COMPOSE_DOWN_ACTION := $(if $(SERVICES),stop,down)
_COMPOSE_DOWN_SERVICES := $(if $(SERVICES),$(subst $(comma), ,$(SERVICES)),)
_COMPOSE_BIND_CRITICAL_READ_PATHS := \
	backend/scan-control-plane/migrations \
	backend/scan-control-plane/scripts \
	backend/file-watcher/configs \
	db-init \
	kong/plugins \
	plugins \
	scripts/db-bootstrap.sh \
	kong.yml \
	redis-users.acl
_COMPOSE_BIND_BEST_EFFORT_READ_PATHS := \
	algorithm \
	api/backend \
	evo

# Only init submodules when not yet cloned; if already present (even with different commit), do nothing. Never recursive.
_SUBMODULE_INIT = @git submodule status | grep -q '^-' && git submodule update --init || true

build:
	$(_SUBMODULE_INIT)
	@$(MAKE) --no-print-directory compose-host-permissions
	@$(_COMPOSE) $(strip $(if $(_need_mineru),--profile mineru)) build \
		$(if $(SERVICES),$(subst $(comma), ,$(SERVICES)),)

compose-host-permissions:
	@echo "🔐 Ensuring compose bind mounts are readable by containers..."
	@chmod a+rx .
	@for path in $(_COMPOSE_BIND_CRITICAL_READ_PATHS); do \
		if [ -e "$$path" ]; then \
			chmod -R a+rX "$$path"; \
		fi; \
	done
	@for path in $(_COMPOSE_BIND_BEST_EFFORT_READ_PATHS); do \
		if [ -e "$$path" ]; then \
			chmod -R a+rX "$$path" 2>/dev/null || true; \
		fi; \
	done

file-watcher-dirs:
	@mkdir -p "$(LAZYMIND_FILE_WATCHER_BASE_ROOT_ABS)" "$(LAZYMIND_FILE_WATCHER_BASE_ROOT_ABS)/staging" "$(LAZYMIND_FILE_WATCHER_BASE_ROOT_ABS)/snapshots" "$(LAZYMIND_FILE_WATCHER_BASE_ROOT_ABS)/logs" "$(LAZYMIND_FILE_WATCHER_BASE_ROOT_ABS)/run" "$(LAZYMIND_FILE_WATCHER_WATCH_HOST_DIR)"

file-watcher-build: file-watcher-stop file-watcher-dirs
	@echo "🔨 Rebuilding file-watcher..."
	@rm -f "$(LAZYMIND_FILE_WATCHER_BIN)"
	@cd "$(LAZYMIND_FILE_WATCHER_DIR)" && $(GO) build -o file_watcher ./cmd/main.go
	@echo "✅ file-watcher built: $(LAZYMIND_FILE_WATCHER_BIN)"

file-watcher-stop:
	@if [ -f "$(LAZYMIND_FILE_WATCHER_PID)" ]; then \
		pid=$$(cat "$(LAZYMIND_FILE_WATCHER_PID)"); \
		if [ -n "$$pid" ] && kill -0 "$$pid" 2>/dev/null; then \
			echo "🛑 Stopping file-watcher ($$pid)..."; \
			kill "$$pid"; \
			for i in 1 2 3 4 5; do \
				kill -0 "$$pid" 2>/dev/null || break; \
				sleep 1; \
			done; \
			if kill -0 "$$pid" 2>/dev/null; then \
				echo "⚠️  file-watcher still running ($$pid), please stop it manually if needed."; \
			fi; \
		fi; \
		rm -f "$(LAZYMIND_FILE_WATCHER_PID)"; \
	fi
	@if command -v lsof >/dev/null 2>&1; then \
		for pid in $$(lsof -t -nP -iTCP:19090 -sTCP:LISTEN 2>/dev/null | sort -u); do \
			cmd=$$(ps -p "$$pid" -o command= 2>/dev/null || true); \
			case "$$cmd" in \
				*file_watcher*) \
					echo "🛑 Stopping host file-watcher on :19090 ($$pid)..."; \
					kill "$$pid" 2>/dev/null || true; \
					;; \
			esac; \
		done; \
	fi

file-watcher-run: file-watcher-stop file-watcher-dirs
	@echo "🚀 Starting file-watcher (LAZYMIND_FILE_WATCHER_BASE_ROOT=$(LAZYMIND_FILE_WATCHER_BASE_ROOT_ABS))..."
	@LAZYMIND_FILE_WATCHER_BASE_ROOT="$(LAZYMIND_FILE_WATCHER_BASE_ROOT_ABS)" nohup sh -c 'cd "$(LAZYMIND_FILE_WATCHER_DIR)" && exec ./file_watcher -config configs/agent.yaml' >> "$(LAZYMIND_FILE_WATCHER_CONSOLE_LOG)" 2>&1 & echo $$! > "$(LAZYMIND_FILE_WATCHER_PID)"
	@sleep 1
	@pid=$$(cat "$(LAZYMIND_FILE_WATCHER_PID)"); \
	if kill -0 "$$pid" 2>/dev/null; then \
		echo "✅ file-watcher started ($$pid), log: $(LAZYMIND_FILE_WATCHER_CONSOLE_LOG)"; \
	else \
		echo "❌ file-watcher failed to start. Recent log:"; \
		tail -n 80 "$(LAZYMIND_FILE_WATCHER_CONSOLE_LOG)" 2>/dev/null || true; \
		rm -f "$(LAZYMIND_FILE_WATCHER_PID)"; \
		exit 1; \
	fi

file-watcher-start: file-watcher-build
	@$(MAKE) --no-print-directory file-watcher-run

desktop-stop-if-present:
	@if [ -x scripts/desktop-down.sh ]; then \
		echo "🛑 Stopping Desktop runtime via scripts/desktop-down.sh..."; \
		scripts/desktop-down.sh || true; \
	elif [ -x desktop/scripts/down.sh ]; then \
		echo "🛑 Stopping Desktop runtime via desktop/scripts/down.sh..."; \
		desktop/scripts/down.sh || true; \
	else \
		echo "ℹ️  No Desktop stop hook found; skipping"; \
	fi

up:
	@if [ "$(LAZYMIND_FILE_WATCHER_MODE)" = "container" ]; then \
		$(MAKE) --no-print-directory file-watcher-stop; \
		$(MAKE) --no-print-directory file-watcher-dirs; \
	else \
		$(MAKE) --no-print-directory file-watcher-build; \
	fi
	$(_SUBMODULE_INIT)
	@$(MAKE) --no-print-directory compose-host-permissions
	@$(_COMPOSE) $(_COMPOSE_PROFILES) up $(_COMPOSE_FILE_WATCHER_SCALE) -d \
		$(if $(SERVICES),$(subst $(comma), ,$(SERVICES)),)
	@if [ "$(LAZYMIND_FILE_WATCHER_MODE)" != "container" ]; then \
		$(MAKE) --no-print-directory file-watcher-run; \
	else \
		echo "✅ file-watcher container enabled"; \
	fi

down:
	@$(MAKE) --no-print-directory file-watcher-stop
	@if [ -x "$(LAZYMIND_LOCAL_BIN)" ]; then \
		"$(LAZYMIND_LOCAL_BIN)" down --profile "$(LAZYMIND_LOCAL_PROFILE)" || true; \
	else \
		echo "ℹ️  No Local Runtime manager found; skipping"; \
	fi
	@$(MAKE) --no-print-directory desktop-stop-if-present
	@echo "🛑 Stopping local compose stack, if present..."
	@$(_COMPOSE_LOCAL) $(_CLEANUP_COMPOSE_PROFILES) $(_COMPOSE_DOWN_ACTION) \
		$(_COMPOSE_DOWN_SERVICES) || true
	@echo "🛑 Stopping default Cloud/Kong compose stack, if present..."
	@$(_COMPOSE_DEFAULT) $(_CLEANUP_COMPOSE_PROFILES) $(_COMPOSE_DOWN_ACTION) \
		$(_COMPOSE_DOWN_SERVICES) || true

up-build:
	@if [ "$(LAZYMIND_FILE_WATCHER_MODE)" = "container" ]; then \
		$(MAKE) --no-print-directory file-watcher-stop; \
		$(MAKE) --no-print-directory file-watcher-dirs; \
	else \
		$(MAKE) --no-print-directory file-watcher-build; \
	fi
	$(_SUBMODULE_INIT)
	@$(MAKE) --no-print-directory compose-host-permissions
	@$(_COMPOSE) $(_COMPOSE_PROFILES) up $(_COMPOSE_FILE_WATCHER_SCALE) --build -d \
		$(if $(SERVICES),$(subst $(comma), ,$(SERVICES)),)
	@if [ "$(LAZYMIND_FILE_WATCHER_MODE)" != "container" ]; then \
		$(MAKE) --no-print-directory file-watcher-run; \
	else \
		echo "✅ file-watcher container enabled"; \
	fi

up-build-local:
	@if [ ! -x "$(PROCESS_COMPOSE_BIN)" ]; then \
		mkdir -p "$(dir $(PROCESS_COMPOSE_BIN))"; \
		GOBIN="$(CURDIR)/local/bin" $(GO) install "$(PROCESS_COMPOSE_PKG)"; \
	fi
	@mkdir -p "$(LAZYMIND_LOCAL_GOCACHE)"
	@cd local/local-runtime-manager && GOCACHE="$(LAZYMIND_LOCAL_GOCACHE)" $(GO) build -buildvcs=false -o lazymind-local .
	@"$(LAZYMIND_LOCAL_BIN)" up --profile "$(LAZYMIND_LOCAL_PROFILE)"

clear:
	@if [ "$(LAZYMIND_FILE_WATCHER_MODE)" != "container" ]; then \
		$(MAKE) --no-print-directory file-watcher-stop; \
	fi
	@echo "🧹 Stopping containers and removing volumes (keeping built images/base cache)..."
	@$(_COMPOSE) $(_CLEANUP_COMPOSE_PROFILES) down -v 2>/dev/null || true
	@echo "🧹 Clearing Python cache..."
	@find . -type d -name '__pycache__' ! -path '*/\.git/*' -exec rm -rf {} + 2>/dev/null || true
	@find . -type f -name '*.pyc' ! -path '*/\.git/*' -delete 2>/dev/null || true
	@echo "✅ Clear done."

# ---------------------------------------------------------------------------
# reset-kb: wipe knowledge-base data only (Milvus, OpenSearch, uploads, and
#           KB-related PostgreSQL tables).  User accounts, auth tokens, Redis,
#           conversations, and prompts are preserved.
#
# PostgreSQL tables cleared (core DB):
#   datasets, default_datasets, documents, tasks, upload_sessions,
#   uploaded_files, acl_kbs
# PostgreSQL tables cleared (app/lazyllm DB — lazyllm-managed):
#   lazyllm_documents, lazyllm_doc_service_tasks,
#   lazyllm_kb_documents, lazyllm_kb_algorithm
#
# After this, run: make up LAZYMIND_RESET_ALGO_ON_STARTUP=true
# ---------------------------------------------------------------------------
_KB_VOLUMES := milvus-etcd milvus-minio milvus-data opensearch-data rag-uploads

# SQL run inside the running db container (or via docker run if db is stopped).
# TRUNCATE … CASCADE handles FK dependencies automatically.
define _RESET_KB_SQL_CORE
TRUNCATE TABLE
  public.tasks,
  public.upload_sessions,
  public.uploaded_files,
  public.documents,
  public.acl_kbs,
  public.default_datasets,
  public.datasets
CASCADE;
endef
export _RESET_KB_SQL_CORE

# Drop all lazyllm-managed tables so SqlManager recreates them with the
# latest schema on next startup.  Must be done via psql BEFORE processor-server
# starts, because processor-server caches ORM metadata at startup and won't
# pick up schema changes if tables are dropped after it has already launched.
define _RESET_KB_SQL_APP
DROP TABLE IF EXISTS
  public.lazyllm_doc_node_group_status,
  public.lazyllm_doc_parse_state,
  public.lazyllm_kb_algorithm,
  public.lazyllm_kb_documents,
  public.lazyllm_knowledge_bases,
  public.lazyllm_doc_path_locks,
  public.lazyllm_documents,
  public.lazyllm_doc_service_tasks,
  public.lazyllm_callback_records,
  public.lazyllm_idempotency_records,
  public.lazyllm_node_group,
  public.lazyllm_algorithm,
  public.lazyllm_waiting_task_queue,
  public.lazyllm_finished_task_queue
CASCADE;
endef
export _RESET_KB_SQL_APP

reset-kb:
	@if [ "$(LAZYMIND_FILE_WATCHER_MODE)" != "container" ]; then \
		$(MAKE) --no-print-directory file-watcher-stop; \
	fi
	@echo "⏹  Stopping all services (keeping db running for SQL cleanup)..."
	@$(_COMPOSE) $(_CLEANUP_COMPOSE_PROFILES) stop \
		lazyllm-algo lazyllm-doc-server lazyllm-parse-server lazyllm-parse-worker \
		chat core frontend kong 2>/dev/null || true
	@echo "🗑  Clearing KB tables in PostgreSQL (core DB)..."
	@$(_COMPOSE) exec -T db psql -U root -d core -c "$$_RESET_KB_SQL_CORE" 2>&1 || \
		echo "⚠️  core DB not running or tables not found — skipping"
	@echo "🗑  Dropping lazyllm schema tables in PostgreSQL (app DB)..."
	@$(_COMPOSE) exec -T db psql -U root -d app -c "$$_RESET_KB_SQL_APP" 2>&1 || \
		echo "⚠️  app DB not running or tables not found — skipping"
	@echo "⏹  Stopping remaining services..."
	@$(_COMPOSE) $(_CLEANUP_COMPOSE_PROFILES) down 2>/dev/null || true
	@echo "🗑  Removing KB volumes: $(_KB_VOLUMES)..."
	@for vol in $(_KB_VOLUMES); do \
		full="$$(docker volume ls -q | grep -E "(^|_)$${vol}$$" | head -1)"; \
		if [ -n "$$full" ]; then \
			docker volume rm "$$full" && echo "  removed $$full" || echo "  skip $$full (in use?)"; \
		else \
			echo "  skip $$vol (not found)"; \
		fi; \
	done
	@echo "🗑  Removing local upload cache..."
	@rm -rf data/core/uploads 2>/dev/null || true
	@echo "✅ KB data cleared."

# ---------------------------------------------------------------------------
# reset-all: wipe ALL persistent data — equivalent to a clean first-run state.
#            Builds on reset-kb and additionally removes pgdata and redisdata.
# ---------------------------------------------------------------------------
reset-all: reset-kb
	@echo "🗑  Removing all remaining persistent volumes (pgdata, redisdata, caches)..."
	@$(_COMPOSE) $(_CLEANUP_COMPOSE_PROFILES) down -v 2>/dev/null || true
	@echo "🧹 Clearing Python cache..."
	@find . -type d -name '__pycache__' ! -path '*/\.git/*' -exec rm -rf {} + 2>/dev/null || true
	@find . -type f -name '*.pyc' ! -path '*/\.git/*' -delete 2>/dev/null || true
	@echo "✅ Full reset done. All persistent data removed."

# ---------------------------------------------------------------------------
# fresh-start: reset-kb + up with LAZYMIND_RESET_ALGO_ON_STARTUP=true.
#
# This is the standard "wipe everything KB-related and restart clean" flow.
# reset-kb alone is not enough: lazyllm_* table schemas are only rebuilt by
# the algo service on startup when LAZYMIND_RESET_ALGO_ON_STARTUP=true.
# ---------------------------------------------------------------------------
fresh-start: reset-kb
	@echo "🚀 Rebuilding images and starting services with LAZYMIND_RESET_ALGO_ON_STARTUP=true..."
	@$(MAKE) --no-print-directory up-build LAZYMIND_RESET_ALGO_ON_STARTUP=true
