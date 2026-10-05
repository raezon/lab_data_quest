# CloudQuest — raccourcis d'installation et de développement.
# `make` (ou `make help`) affiche la liste des commandes.

COMPOSE ?= docker compose
GO      ?= go
BIN     := bin/cloudquest

S3_ENDPOINT     ?= http://127.0.0.1:9100
DYNAMO_ENDPOINT ?= http://127.0.0.1:8100
SQS_ENDPOINT    ?= http://127.0.0.1:9324

# Arguments passés aux clients : make s3 ARGS="ls"
ARGS ?=

.DEFAULT_GOAL := help

.PHONY: help
help: ## Affiche cette aide
	@awk 'BEGIN {FS = ":.*## "} \
		/^##@/ {printf "\n\033[1m%s\033[0m\n", substr($$0, 5)} \
		/^[a-zA-Z0-9_-]+:.*## / {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

##@ Installation

.PHONY: check
check: ## Vérifie les prérequis (Docker, Compose, Go)
	@command -v docker >/dev/null || { echo "✗ Docker introuvable : https://docs.docker.com/get-docker/"; exit 1; }
	@$(COMPOSE) version >/dev/null 2>&1 || { echo "✗ Docker Compose introuvable : https://docs.docker.com/compose/install/"; exit 1; }
	@docker info >/dev/null 2>&1 || { echo "✗ Le démon Docker ne répond pas (est-il démarré ? droits du groupe docker ?)"; exit 1; }
	@command -v $(GO) >/dev/null || { echo "✗ Go introuvable (≥ 1.24) : https://go.dev/dl/"; exit 1; }
	@echo "✓ $$(docker --version)"
	@echo "✓ $$($(COMPOSE) version)"
	@echo "✓ $$($(GO) version)"

.PHONY: install
install: check ## Installe tout : modules Go + images Docker (émulateurs et clients)
	$(GO) mod download
	$(COMPOSE) --profile tools pull
	@echo "✓ Installation terminée — lancez « make start »"

##@ Émulateurs

.PHONY: up
up: ## Démarre MinIO, DynamoDB Local et ElasticMQ
	$(COMPOSE) up -d --wait

.PHONY: down
down: ## Arrête les émulateurs (tables et files en mémoire perdues)
	$(COMPOSE) down

.PHONY: ps
ps: ## État des conteneurs
	$(COMPOSE) ps

.PHONY: logs
logs: ## Suit les journaux des émulateurs
	$(COMPOSE) logs -f

##@ Jeu

.PHONY: run
run: ## Lance le jeu : http://127.0.0.1:8090
	$(GO) run ./cmd/cloudquest

.PHONY: start
start: up run ## Démarre les émulateurs puis le jeu

.PHONY: build
build: ## Compile le binaire dans bin/
	$(GO) build -o $(BIN) ./cmd/cloudquest

.PHONY: pipeline
pipeline: ## Lance le worker du projet final (projet/pipeline)
	$(GO) run ./projet/pipeline

##@ Clients (ex. : make s3 ARGS="ls")

.PHONY: mc
mc: ## Client MinIO : make mc ARGS="ls local"
	$(COMPOSE) run --rm mc $(ARGS)

.PHONY: aws
aws: ## AWS CLI brute : make aws ARGS="s3 ls --endpoint-url ..."
	$(COMPOSE) run --rm aws $(ARGS)

.PHONY: s3
s3: ## aws s3 sur MinIO : make s3 ARGS="ls"
	$(COMPOSE) run --rm aws s3 $(ARGS) --endpoint-url $(S3_ENDPOINT)

.PHONY: s3api
s3api: ## aws s3api sur MinIO : make s3api ARGS="list-buckets"
	$(COMPOSE) run --rm aws s3api $(ARGS) --endpoint-url $(S3_ENDPOINT)

.PHONY: dynamo
dynamo: ## aws dynamodb local : make dynamo ARGS="list-tables"
	$(COMPOSE) run --rm aws dynamodb $(ARGS) --endpoint-url $(DYNAMO_ENDPOINT)

.PHONY: sqs
sqs: ## aws sqs sur ElasticMQ : make sqs ARGS="list-queues"
	$(COMPOSE) run --rm aws sqs $(ARGS) --endpoint-url $(SQS_ENDPOINT)

##@ Qualité

.PHONY: fmt
fmt: ## Formate le code Go
	$(GO) fmt ./...

.PHONY: vet
vet: ## Analyse statique (go vet)
	$(GO) vet ./...

.PHONY: test
test: ## Lance les tests (émulateurs démarrés requis)
	$(GO) test ./...

.PHONY: ci
ci: fmt vet test ## fmt + vet + test

##@ Divers

.PHONY: pdf
pdf: ## Régénère docs/cahier-de-projet.pdf (weasyprint requis)
	@command -v weasyprint >/dev/null || { echo "✗ weasyprint introuvable : pip install weasyprint"; exit 1; }
	weasyprint docs/cahier-de-projet.html docs/cahier-de-projet.pdf

.PHONY: clean
clean: ## Supprime le binaire compilé
	rm -rf bin

.PHONY: reset
reset: ## Repart de zéro : supprime volumes ET progression (CONFIRM=1 requis)
	@[ "$(CONFIRM)" = "1" ] || { echo "Ceci efface les buckets MinIO et data/progress.json."; echo "Relancez avec : make reset CONFIRM=1"; exit 1; }
	$(COMPOSE) down -v
	rm -rf data
