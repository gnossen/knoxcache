SRCS=$(shell find . -name '*.go')

.PHONY: fmt
fmt: ${SRCS}
	echo ${SRCS}
	for SRC in ${SRCS}; do \
		go fmt $${SRC}; \
	done
