REGISTRY  ?= docker.io
NAMESPACE ?= aijutsudev
IMAGE     ?= ytsrtgen
BUILD_DATE ?= $(shell date -u +%Y%m%d)
COMMIT_SHA ?= $(shell git rev-parse --verify HEAD 2>/dev/null | cut -c1-8)
TAG       ?= $(if $(COMMIT_SHA),$(BUILD_DATE)-$(COMMIT_SHA),$(BUILD_DATE)-dev)
PLATFORMS ?= linux/amd64,linux/arm64
PORT      ?= 8080

IMAGE_REF       := $(REGISTRY)/$(NAMESPACE)/$(IMAGE)
IMAGE_REF_TAG   := $(IMAGE_REF):$(TAG)
IMAGE_REF_LATEST:= $(IMAGE_REF):latest

.DEFAULT_GOAL := help

.PHONY: help
help: ## List available targets
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[1m%-18s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

## --- Go ---

.PHONY: tidy
tidy: ## Run go mod tidy
	go mod tidy

SWAG ?= $(shell command -v swag 2>/dev/null)

.PHONY: swagger
swagger: ## Regenerate swagger docs into ./docs (installs github.com/swaggo/swag/cmd/swag if missing)
	@if [ -z "$(SWAG)" ]; then \
		echo "swag not found; installing github.com/swaggo/swag/cmd/swag@latest"; \
		go install github.com/swaggo/swag/cmd/swag@latest; \
	fi
	$(or $(SWAG),$(shell go env GOPATH)/bin/swag) init --parseDependency --parseInternal -g main.go -o ./docs

.PHONY: build-go
build-go: swagger ## Build the Go binary on the host (./bin/ytsrtgen)
	mkdir -p ./bin
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ./bin/ytsrtgen .

.PHONY: test
test: ## Run go tests
	go test ./...

## --- Docker (single-arch, local load) ---

.PHONY: build
build: swagger ## Build the image for the local arch and load into the docker daemon
	docker build -t $(IMAGE_REF_TAG) -t $(IMAGE_REF_LATEST) .

.PHONY: run
run: ## Run the image locally on $(PORT)
	docker run --rm -p $(PORT):8080 \
		--read-only --tmpfs /tmp \
		--cap-drop=ALL \
		--security-opt=no-new-privileges \
		$(IMAGE_REF_TAG)

.PHONY: shell
shell: ## Open a shell inside a fresh container (for debugging)
	docker run --rm -it --entrypoint /bin/sh $(IMAGE_REF_TAG)

## --- Docker (multi-arch via buildx) ---

.PHONY: buildx-setup
buildx-setup: ## Create and use a buildx builder named "ytsrtgen"
	docker buildx inspect ytsrtgen >/dev/null 2>&1 || \
		docker buildx create --name ytsrtgen --use
	docker buildx use ytsrtgen
	docker buildx inspect --bootstrap

.PHONY: buildx
buildx: swagger buildx-setup ## Build multi-arch image without pushing (cached only)
	docker buildx build \
		--platform $(PLATFORMS) \
		-t $(IMAGE_REF_TAG) \
		-t $(IMAGE_REF_LATEST) \
		.

## --- Publish ---

.PHONY: login
login: ## Log into $(REGISTRY) (uses $$CR_PAT for ghcr.io if set, else prompts)
	@if [ "$(REGISTRY)" = "ghcr.io" ] && [ -n "$$CR_PAT" ]; then \
		echo "$$CR_PAT" | docker login $(REGISTRY) -u $(NAMESPACE) --password-stdin; \
	else \
		docker login $(REGISTRY); \
	fi

.PHONY: push
push: ## Push the locally-built tags ($(TAG) and latest)
	docker push $(IMAGE_REF_TAG)
	docker push $(IMAGE_REF_LATEST)

.PHONY: publish
publish: swagger buildx-setup ## Build multi-arch and push in one step
	docker buildx build \
		--platform $(PLATFORMS) \
		-t $(IMAGE_REF_TAG) \
		-t $(IMAGE_REF_LATEST) \
		--push \
		.

## --- Helm ---

HELM_CHART     ?= ./charts/ytsrtgen
HELM_RELEASE   ?= ytsrtgen
HELM_NAMESPACE ?= ytsrtgen
HELM_VALUES    ?=
KUBE_CONTEXT   ?=

HELM_CTX_FLAG     := $(if $(KUBE_CONTEXT),--kube-context $(KUBE_CONTEXT),)
HELM_VALUES_FLAG  := $(if $(HELM_VALUES),-f $(HELM_VALUES),)
HELM_IMAGE_FLAGS  := --set image.repository=$(IMAGE_REF) --set image.tag=$(TAG)

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart
	helm lint $(HELM_CHART) $(HELM_VALUES_FLAG) $(HELM_IMAGE_FLAGS)

.PHONY: helm-template
helm-template: ## Render the chart locally (no cluster contact)
	helm template $(HELM_RELEASE) $(HELM_CHART) \
		--namespace $(HELM_NAMESPACE) \
		$(HELM_VALUES_FLAG) $(HELM_IMAGE_FLAGS)

.PHONY: helm-diff
helm-diff: ## Show what would change vs. the live release (requires helm-diff plugin)
	helm diff upgrade $(HELM_RELEASE) $(HELM_CHART) \
		$(HELM_CTX_FLAG) \
		--namespace $(HELM_NAMESPACE) \
		$(HELM_VALUES_FLAG) $(HELM_IMAGE_FLAGS) \
		--allow-unreleased

.PHONY: helm-deploy
helm-deploy: ## Install or upgrade the release on the current cluster
	helm upgrade --install $(HELM_RELEASE) $(HELM_CHART) \
		$(HELM_CTX_FLAG) \
		--namespace $(HELM_NAMESPACE) --create-namespace \
		$(HELM_VALUES_FLAG) $(HELM_IMAGE_FLAGS) \
		--atomic --wait

.PHONY: helm-uninstall
helm-uninstall: ## Remove the release from the current cluster
	helm uninstall $(HELM_RELEASE) \
		$(HELM_CTX_FLAG) \
		--namespace $(HELM_NAMESPACE)

.PHONY: helm-package
helm-package: ## Package the chart into ./dist
	mkdir -p ./dist
	helm package $(HELM_CHART) --destination ./dist

## --- House-keeping ---

.PHONY: print
print: ## Print resolved image and helm references
	@echo "IMAGE_REF_TAG    = $(IMAGE_REF_TAG)"
	@echo "IMAGE_REF_LATEST = $(IMAGE_REF_LATEST)"
	@echo "PLATFORMS        = $(PLATFORMS)"
	@echo "HELM_CHART       = $(HELM_CHART)"
	@echo "HELM_RELEASE     = $(HELM_RELEASE)"
	@echo "HELM_NAMESPACE   = $(HELM_NAMESPACE)"
	@echo "KUBE_CONTEXT     = $(if $(KUBE_CONTEXT),$(KUBE_CONTEXT),<current>)"

.PHONY: clean
clean: ## Remove host artifacts and local image tags
	rm -rf ./bin ./dist
	-docker image rm $(IMAGE_REF_TAG) $(IMAGE_REF_LATEST) 2>/dev/null
