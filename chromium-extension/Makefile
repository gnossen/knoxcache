LOGOS=logo16.png logo32.png logo64.png
RESOURCES=knox.js knox.css knox.html manifest.json ${LOGOS}

logo16.png: ../logo.svg
	inkscape -w 16 -h 16 $^ -o $@

logo32.png: ../logo.svg
	inkscape -w 32 -h 32 $^ -o $@

logo64.png: ../logo.svg
	inkscape -w 64 -h 64 $^ -o $@

.PHONY: logos
logos: ${LOGOS}

knox-chromium-extension.zip: ${RESOURCES}
	zip $@ $<

.PHONY: all
all: knox-chromium-extension.zip

.PHONY: clean
clean:
	rm -f *.png
	rm -f *.zip
