
mkfile_path := $(word $(words $(MAKEFILE_LIST)),$(MAKEFILE_LIST))
source_dir:=$(shell cd $(shell dirname $(mkfile_path)); pwd)


executable_name := "kubernetes-watchdog"
code_path := "github.com/Azure/kubernetes-watchdog"


ifeq ($(OS),Windows_NT)
	GO_BUILD_MODE = default
else
	UNAME_S := $(shell uname -s)
	ifeq ($(UNAME_S), Linux)
		GO_BUILD_MODE = pie
	endif
	ifeq ($(UNAME_S), Darwin)
		GO_BUILD_MODE = default
	endif
endif


VERSION_VAR := $(source_dir)/version.Version
VERSION_VAL := 0.1
GIT_VAR := $(source_dir)/version.GitCommit
BUILD_DATE_VAR := $(source_dir)/version.BuildDate
BUILD_DATE := $$(date +%Y-%m-%d-%H:%M)
GIT_HASH := $$(git rev-parse --short HEAD)
VK_CHECK=kubectl get nodes -o jsonpath='{.items[*].metadata.labels.type}'


go_build_flags := -buildmode=${GO_BUILD_MODE} -ldflags "-s -X $(VERSION_VAR)=$(VERSION_VAL) -X $(GIT_VAR)=$(GIT_HASH) -X $(BUILD_DATE_VAR)=$(BUILD_DATE)"

.PHONY: clean build vnode-deploy


build: 
	@mkdir -p "$(source_dir)/output"
	@docker run --rm -v "$(source_dir)":/go/src/$(code_path)  -v "$(source_dir)/output/":/mnt/output  -w /go/src/$(code_path) golang:1.11 go build -v -o /mnt/output/$(executable_name) $(go_build_flags)

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

vnode-deploy:
	@if [ -z "${CLIENT_ID}" ]; then \
		echo "CLIENT_ID must be set to the Service Principal client id" && exit 1;\
	fi
	@if [ -z "${CLIENT_PASSWORD}" ]; then \
		echo "CLIENT_PASSWORD must be set to the Service Principal client secret" && exit 1;\
	fi
	@if [ -z "${TENANT_ID}" ]; then \
		echo "TENANT_ID must be set" && exit 1;\
	fi
	@if [ -z "${AKS_NAME}" ]; then \
		echo "AKS_NAME must be set" && exit 1;\
	fi
	@if [ -z "${AKS_GROUP}" ]; then \
		echo "TENANT_ID must be set" && exit 1;\
	fi
	@if [ "$(shell $(VK_CHECK))" != "virtual-kubelet" ]; then \
		echo "Virtual Node addon must be installed to AKS instance ${AKS_NAME}" && exit 1;\
	fi

	@az aks get-credentials -n ${AKS_NAME} -g ${AKS_GROUP} -f config
	@kubectl create secret generic kube-watchdog -n kube-system --from-file=./config
	@kubectl create secret generic kube-watchdog-aadclient -n kube-system \
		--from-literal=clientid=${CLIENT_ID} \
		--from-literal=clientpassword=${CLIENT_PASSWORD} \
		--from-literal=tenantid=${TENANT_ID}
	@rm ./config
	@kubectl apply -f $(source_dir)/build-test-tools/deploy/vnode-deploy.yaml

clean-deploy:
	@kubectl delete deploy kube-watchdog -n kube-system
	@kubectl delete secrets kube-watchdog kube-watchdog-aadclient -n kube-system
	@rm ./config
