NAME ?= world
GREETING := hello
MSG = $(GREETING), $(NAME)

.PHONY: hello where state fail ignore all first second

hello: ## Print the greeting.
	@echo "$(MSG)"

# A directory change lasts for one line.
where:
	@cd sub
	@echo "$${PWD##*/}"

state:
	@export OTHER=one
	@echo "other=$${OTHER:-unset}"

fail:
	@echo before
	@exit 3
	@echo after

ignore:
	-@false
	@echo after

all: first second
	@echo all

first:
	@echo first

second: first
	@echo second
