NOW := $(shell touch MARKER)
HASH != touch MARKER
include missing.mk
-include $(shell touch MARKER).mk

.PHONY: build
build:
	touch MARKER
