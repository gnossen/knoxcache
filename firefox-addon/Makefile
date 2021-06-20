BUILD_NUMBER ?= dev

# TODO: Proxy the major version from the chromium extension.
VERSION="0.1.${BUILD_NUMBER}"

LOGO_SIZES=16 32 64
LOGOS=$(foreach SIZE,${LOGO_SIZES},logo${SIZE}.png )
LOGO_EXPRESSION=$(shell echo ${LOGO_SIZES} | awk '{for(i=1; i<=NF; i++) {printf "{\"icons\": {\"" $$i "\": \"logo" $$i ".png\"}}"; if (i != NF) {printf " * "} }}')
JQ_CMD='. + {version: $$version} * ${LOGO_EXPRESSION}'

COPIED_RESOURCES=knox.js knox.css knox.html
RESOURCES=manifest.json ${COPIED_RESOURCES} ${LOGOS}

.PHONY: all
all: knox-firefox-addon-${VERSION}.xpi

manifest.json: manifest.json.template
	jq --arg version ${VERSION} ${JQ_CMD} <"$^" >"$@"

define copied_resource_template =
$(eval RESOURCE_NAME		:= $(1))

$(RESOURCE_NAME): ../chromium-extension/$(RESOURCE_NAME)
	cp "$$^" "$$@"
endef

$(foreach RESOURCE_NAME,${COPIED_RESOURCES},$(eval $(call copied_resource_template,${RESOURCE_NAME})))

define logo_template =
$(eval LOGO_SIZE		:= $(1))
$(eval LOGO := logo$(LOGO_SIZE).png)

$(LOGO): ../logo.svg
	inkscape -w ${LOGO_SIZE} -h ${LOGO_SIZE} "$$^" -o "$$@"
endef

$(foreach LOGO_SIZE,${LOGO_SIZES},$(eval $(call logo_template,${LOGO_SIZE})))

.PHONY: logos
logos: ${LOGOS}

knox-firefox-addon-${VERSION}.xpi: ${RESOURCES}
	zip $@ $^

.PHONY: print-version
print-version:
	@echo "${VERSION}"

.PHONY: clean
clean:
	rm -f *.png
	rm -f *.xpi
	rm -f ${COPIED_RESOURCES}
	rm manifest.json
