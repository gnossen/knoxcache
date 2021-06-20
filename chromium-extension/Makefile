BUILD_NUMBER ?= dev
VERSION="0.1.${BUILD_NUMBER}"

LOGO_SIZES=16 32 64
LOGOS=$(foreach SIZE,${LOGO_SIZES},logo${SIZE}.png )
LOGO_EXPRESSION=$(shell echo ${LOGO_SIZES} | awk '{for(i=1; i<=NF; i++) {printf "{\"icons\": {\"" $$i "\": \"logo" $$i ".png\"}}"; if (i != NF) {printf " * "} }}')
JQ_CMD='. + {version: $$version} * ${LOGO_EXPRESSION}'

RESOURCES=knox.js knox.css knox.html manifest.json ${LOGOS}

.PHONY: all
all: knox-chromium-extension-${VERSION}.zip

manifest.json: manifest.json.template
	jq --arg version ${VERSION} ${JQ_CMD} <"$^" >"$@"


define logo_template =
$(eval LOGO_SIZE		:= $(1))
$(eval LOGO := logo$(LOGO_SIZE).png)

$(LOGO): ../logo.svg
	inkscape -w ${LOGO_SIZE} -h ${LOGO_SIZE} "$$^" -o "$$@"
endef

$(foreach LOGO_SIZE,${LOGO_SIZES},$(eval $(call logo_template,${LOGO_SIZE})))

.PHONY: logos
logos: ${LOGOS}

knox-chromium-extension-${VERSION}.zip: ${RESOURCES}
	zip $@ $<

.PHONY: print-version
print-version:
	@echo "${VERSION}"

.PHONY: clean
clean:
	rm -f *.png
	rm -f *.zip
	rm manifest.json
