APP := listen-party
BUILD_DIR := build
PUBLISH_DIR := publish
CONFIG_ROOT ?= $(if $(XDG_CONFIG_HOME),$(XDG_CONFIG_HOME),$(HOME)/.config)
CONFIG_DIR ?= $(CONFIG_ROOT)/listen-party
PACKAGE_NAME := $(APP)-$(shell date +%Y%m%d-%H%M%S)
PACKAGE_DIR := $(PUBLISH_DIR)/$(PACKAGE_NAME)

.PHONY: run compile package

run: compile
	@clear 2>/dev/null || true
	./$(BUILD_DIR)/lp

compile:
	@clear 2>/dev/null || true
	date
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o $(BUILD_DIR)/lp .
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o $(BUILD_DIR)/lp.exe .

package: compile
	test -d "$(CONFIG_DIR)" || (echo "config dir not found: $(CONFIG_DIR)" >&2; exit 1)
	rm -rf "$(PACKAGE_DIR)"
	mkdir -p "$(PACKAGE_DIR)/bin" "$(PACKAGE_DIR)/config"
	cp "$(BUILD_DIR)/lp" "$(PACKAGE_DIR)/bin/"
	cp "$(BUILD_DIR)/lp.exe" "$(PACKAGE_DIR)/bin/"
	cp -a "$(CONFIG_DIR)" "$(PACKAGE_DIR)/config/listen-party"
	tar -C "$(PUBLISH_DIR)" -czf "$(PUBLISH_DIR)/$(PACKAGE_NAME).tar.gz" "$(PACKAGE_NAME)"
	rm -rf "$(PACKAGE_DIR)"
	@echo "wrote $(PUBLISH_DIR)/$(PACKAGE_NAME).tar.gz"
	
