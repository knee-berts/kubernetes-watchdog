
mkfile_path := $(word $(words $(MAKEFILE_LIST)),$(MAKEFILE_LIST))
source_dir:=$(shell cd $(shell dirname $(mkfile_path)); pwd)


executable_name := "kubernetes-watchdog"
code_path := "github.com/Azure/kubernetes-watchdog"
go_optimize := ""




.PHONY: clean build


build: 
	@mkdir -p "$(source_dir)/output"
	@docker run --rm -v "$(source_dir)":/go/src/$(code_path)  -v "$(source_dir)/output/":/mnt/output  -w /go/src/$(code_path) golang:1.10-alpine go build -v -o /mnt/output/$(executable_name) $(go_optimize)

clean: 
	@rm -rf "$(source_dir)/output"

container:
	@if ! [ -f "$(source_dir)/output/$(executable_name)" ]; then \
		echo "Executable not found in $(source_dir)/output/" && exit 1;\
	fi
	@if [ -z "${REGISTRY}" ]; then \
		echo "REGISTRY must be set" && exit 1;\
	fi
	@if [ -z "${VERSION}" ]; then \
		echo "VERSION must be set" && exit 1;\
	fi

	@docker build $(source_dir) -t "${REGISTRY}:${VERSION}"
