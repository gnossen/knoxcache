PATCH_VERSION ?= dev
VERSION="0.1.${PATCH_VERSION}"

LOGO_SIZES=16 32 64
LOGOS=$(foreach SIZE,${LOGO_SIZES},logo${SIZE}.png )

RESOURCES=knox.js knox.css knox.html manifest.json ${LOGOS}

.PHONY: all
all: knox-chromium-extension-${VERSION}.zip

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


.PHONY: clean
clean:
	rm -f *.png
	rm -f *.zip
