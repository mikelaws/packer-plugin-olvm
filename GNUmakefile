NAME=olvm
BINARY=packer-plugin-${NAME}

COUNT?=1
TEST?=$(shell go list ./...)
HASHICORP_PACKER_PLUGIN_SDK_VERSION?=$(shell go list -m github.com/hashicorp/packer-plugin-sdk | cut -d " " -f2)
PLUGIN_FQN=$(shell grep -E '^module' <go.mod | sed -E 's/module \s*//')

.PHONY: dev

build:
	@go build -o bin/${BINARY}

dev:
	go build -ldflags="-X '${PLUGIN_FQN}/version.VersionPrerelease=-dev'" -o bin/${BINARY}
	packer plugins install --path ${BINARY} "$(shell echo "${PLUGIN_FQN}" | sed 's/packer-plugin-//')"

test:
	@go test -race -count $(COUNT) $(TEST) -timeout=3m

install-packer-sdc: ## Install packer sofware development command
	@go install github.com/hashicorp/packer-plugin-sdk/cmd/packer-sdc@${HASHICORP_PACKER_PLUGIN_SDK_VERSION}

plugin-check: 
	@if command -v $HOME/go/bin/packer-sdc >/dev/null 2>&1; then \
		PACKER_SDC=$HOME/go/bin/packer-sdc; \
	elif command -v packer-sdc >/dev/null 2>&1; then \
		PACKER_SDC=packer-sdc; \
	else \
		echo "Installing packer-sdc..."; \
		$(MAKE) install-packer-sdc; \
		PACKER_SDC=packer-sdc; \
	fi; \
	if [ -f "packer-plugin-olvm" ]; then \
		chmod +x packer-plugin-olvm; \
		$$PACKER_SDC plugin-check packer-plugin-olvm; \
	elif [ -f "bin/${BINARY}" ]; then \
		$$PACKER_SDC plugin-check bin/${BINARY}; \
	else \
		echo "No plugin binary found. Building first..."; \
		$(MAKE) build; \
		$$PACKER_SDC plugin-check bin/${BINARY}; \
	fi

testacc: dev
	@PACKER_ACC=1 go test -count $(COUNT) -v $(TEST) -timeout=120m

generate: install-packer-sdc
	@go generate ./...
	@rm -rf .docs
	@packer-sdc renderdocs -src docs -partials docs-partials/ -dst .docs/
	@./.web-docs/scripts/compile-to-webdocs.sh "." ".docs" ".web-docs" "hashicorp"
	@rm -r ".docs"
