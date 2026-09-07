SHELL := /bin/sh

COMPOSE ?= docker compose
E2E_BASE_URL ?= http://127.0.0.1:8080
KEEP_STACK ?= 0

.PHONY: help install test test-api test-processor test-frontend test-e2e test-e2e-production test-e2e-production-headed stack-up stack-down rag-eval

help:
	@printf '%s\n' \
		'make install                 Install frontend and processor dependencies' \
		'make test                    Run deterministic project tests' \
		'make test-e2e                Run mocked Playwright tests' \
		'make test-e2e-production     Start Compose and run production-like tests' \
		'make test-e2e-production-headed  Start Compose and run headed production-like tests' \
		'KEEP_STACK=1 make test-e2e-production  Keep Compose running after the test'

install:
	cd frontend && bun install --frozen-lockfile
	cd document-processor && uv sync --locked

test: test-api test-processor test-frontend test-e2e

test-api:
	cd api && go test ./...

test-processor:
	cd document-processor && uv run python -m pytest

test-frontend:
	cd frontend && bun run build && bun run lint

test-e2e:
	cd frontend && bun run test:e2e

stack-up:
	$(COMPOSE) up -d --build

stack-down:
	$(COMPOSE) down

test-e2e-production:
	@set -e; \
	trap 'status=$$?; if [ "$(KEEP_STACK)" != "1" ]; then $(COMPOSE) down; fi; exit $$status' EXIT INT TERM; \
	$(COMPOSE) up -d --build; \
	 i=0; while ! curl --fail --silent --show-error $(E2E_BASE_URL) >/dev/null; do \
		 i=$$((i + 1)); \
		 if [ $$i -ge 120 ]; then printf '%s\n' 'Timed out waiting for the frontend'; exit 1; fi; \
		 sleep 2; \
	done; \
	cd frontend && E2E_BASE_URL=$(E2E_BASE_URL) bun run test:e2e:production

test-e2e-production-headed:
	@set -e; \
	trap 'status=$$?; if [ "$(KEEP_STACK)" != "1" ]; then $(COMPOSE) down; fi; exit $$status' EXIT INT TERM; \
	$(COMPOSE) up -d --build; \
	 i=0; while ! curl --fail --silent --show-error $(E2E_BASE_URL) >/dev/null; do \
		 i=$$((i + 1)); \
		 if [ $$i -ge 120 ]; then printf '%s\n' 'Timed out waiting for the frontend'; exit 1; fi; \
		 sleep 2; \
	done; \
	cd frontend && E2E_BASE_URL=$(E2E_BASE_URL) bun run test:e2e:production -- --headed

rag-eval:
	python scripts/run_rag_eval.py --base-url http://127.0.0.1:1323 --dataset evals/rag_questions.jsonl --documents evals/documents --output evals/results/local.json
