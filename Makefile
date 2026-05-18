REGISTRY  ?= docker.io
NAMESPACE ?= aijutsudev
IMAGE     ?= ytsrtgen
BUILD_DATE ?= $(shell date -u +%Y%m%d)
COMMIT_SHA ?= $(shell git rev-parse --is-inside-work-tree >/dev/null 2>&1 && git rev-parse HEAD | cut -c1-8)
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

.PHONY: build-go
build-go: ## Build the Go binary on the host (./ytsrtgen)
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ./ytsrtgen .

.PHONY: test
test: ## Run go tests
	go test ./...

## --- Docker (single-arch, local load) ---

.PHONY: build
build: ## Build the image for the local arch and load into the docker daemon
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
buildx: buildx-setup ## Build multi-arch image without pushing (cached only)
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
publish: buildx-setup ## Build multi-arch and push in one step
	docker buildx build \
		--platform $(PLATFORMS) \
		-t $(IMAGE_REF_TAG) \
		-t $(IMAGE_REF_LATEST) \
		--push \
		.

## --- House-keeping ---

.PHONY: print
print: ## Print resolved image references
	@echo "IMAGE_REF_TAG    = $(IMAGE_REF_TAG)"
	@echo "IMAGE_REF_LATEST = $(IMAGE_REF_LATEST)"
	@echo "PLATFORMS        = $(PLATFORMS)"

.PHONY: clean
clean: ## Remove the host Go binary and local image tags
	rm -f ./ytsrtgen
	-docker image rm $(IMAGE_REF_TAG) $(IMAGE_REF_LATEST) 2>/dev/null
