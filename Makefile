.PHONY: help mobile mobile-remote admin server test quality docs-check infra-up infra-down infra-status acceptance-local remote-test-validate remote-test-up remote-test-down remote-test-status remote-test-logs production-validate production-config production-deploy production-smoke ip-production-validate ip-production-config ip-production-cert ip-production-deploy backup restore-drill

PROD_ENV ?= .env.production
PROD_COMPOSE = docker compose --env-file $(PROD_ENV) -f infra/compose.production.yaml
REMOTE_TEST_ENV ?= .env.remote-test
REMOTE_TEST_COMPOSE = docker compose --env-file $(REMOTE_TEST_ENV) -f infra/compose.remote-test.yaml

help:
	@echo "mobile                Run the Flutter client"
	@echo "mobile-remote         Run Flutter against remote HTTPS/WSS resources"
	@echo "admin                 Run the operations console"
	@echo "server                Run the Go API in development mode"
	@echo "test                   Run all test suites"
	@echo "quality                Run admin lint/build and all tests"
	@echo "docs-check             Validate local Markdown links and shell syntax"
	@echo "infra-up               Start the local full stack"
	@echo "infra-down             Stop the local full stack without deleting data"
	@echo "infra-status           Show local container health"
	@echo "acceptance-local       Exercise full product journeys on local Docker"
	@echo "remote-test-up         Run only local Web/Admin against remote resources"
	@echo "remote-test-status     Show remote-test frontend container status"
	@echo "remote-test-logs       Follow remote-test frontend logs"
	@echo "production-validate    Validate production secrets and configuration"
	@echo "production-config      Render and validate production Compose"
	@echo "production-deploy      Build, deploy and smoke-test production"
	@echo "ip-production-cert     Issue/renew the short-lived IP TLS certificate"
	@echo "ip-production-deploy   Build, deploy and smoke-test IP production"
	@echo "backup                 Back up PostgreSQL and object storage"

mobile:
	cd apps/mobile && flutter run

mobile-remote:
	REMOTE_TEST_ENV=$(REMOTE_TEST_ENV) infra/scripts/run-mobile-remote.sh

admin:
	cd apps/admin && npm run dev

server:
	cd server && IM_ENV=development IM_ADDR=127.0.0.1:8080 IM_MODE=memory IM_DEV_MODE=true IM_DEV_OTP_CODE=123456 IM_JWT_SECRET=local-development-jwt-secret-change-me IM_ADMIN_EMAIL=admin@nexachat.local IM_ADMIN_PASSWORD_HASH='$$2a$$12$$rAyv6obDffJSqZ1aaqOCR.ER2UXp8ZPsEl2bJCTovnsJJrFshtxNW' IM_ADMIN_TOTP_SECRET=JBSWY3DPEHPK3PXP IM_ADMIN_SHARED_KEY_ENABLED=false IM_PUSH_PROVIDER=log go run ./cmd/server

test:
	cd server && go test ./...
	cd apps/mobile && flutter test
	cd apps/admin && npm test

quality:
	cd apps/admin && npm run lint && npm test && npm run build
	cd server && go test ./...
	cd apps/mobile && flutter analyze && flutter test

docs-check:
	infra/scripts/check-docs.sh
	bash -n infra/scripts/*.sh

infra-up:
	docker compose -f infra/compose.yaml up -d --build

infra-down:
	docker compose -f infra/compose.yaml down

infra-status:
	docker compose -f infra/compose.yaml ps

acceptance-local:
	infra/scripts/acceptance-local.sh

remote-test-validate:
	infra/scripts/validate-remote-test-env.sh $(REMOTE_TEST_ENV)
	$(REMOTE_TEST_COMPOSE) config -q

remote-test-up: remote-test-validate
	docker compose -f infra/compose.yaml stop web admin 2>/dev/null || true
	$(REMOTE_TEST_COMPOSE) up -d --build

remote-test-down:
	$(REMOTE_TEST_COMPOSE) down

remote-test-status:
	$(REMOTE_TEST_COMPOSE) ps

remote-test-logs:
	$(REMOTE_TEST_COMPOSE) logs -f --tail=200 web admin

production-validate:
	infra/scripts/validate-production-env.sh $(PROD_ENV)

production-config: production-validate
	$(PROD_COMPOSE) config -q

production-deploy:
	infra/scripts/deploy.sh $(PROD_ENV)

production-smoke:
	infra/scripts/smoke.sh $(PROD_ENV)

ip-production-validate:
	infra/scripts/validate-production-env.sh $${IP_PROD_ENV:-.env.ip.production}

ip-production-config: ip-production-validate
	docker compose --env-file $${IP_PROD_ENV:-.env.ip.production} -f infra/compose.ip.yaml -f infra/compose.ip.production.yaml config -q

ip-production-cert: ip-production-validate
	infra/scripts/issue-ip-certificate.sh $${IP_PROD_ENV:-.env.ip.production}

ip-production-deploy:
	infra/scripts/deploy-ip-production.sh $${IP_PROD_ENV:-.env.ip.production}

backup:
	infra/scripts/backup.sh $(PROD_ENV)

restore-drill:
	@test -n "$(BACKUP)" || (echo "usage: make restore-drill BACKUP=/absolute/path/postgres.dump" >&2; exit 2)
	infra/scripts/restore-drill.sh $(BACKUP)
