*warning this is an early alpha project, DO NOT use in production*

# What is This?

kubernetes-watchdog watches for unready node. Each unready node is tracked and advanced in a simple state machine
- Phase 0 tracked node (node reaches unready node)
- Phase 1 node ages (no actions)
- Phase 2 node ages further, this where we start performing power check (check if node is powered on)
  - if node is powered on, we wait further if `--force-stagetwo-restart` set we restart the node after `--force-stagetwo-restart-wait` duration
  - if the node is powered off it advances to phase 3
- Phase 3 if the node ages (and still powered off) we detach disks, delete pods and delete the node object from the cluster. This allow pvs to be attached to a different node


> if the node (except aged phase 3) recovers at any phase it is removed from the tracking list. All timing is controlled via command line arguments

# Build
if you have golang tooling then on the root of this repo execute `go build .`

## Build Using Docker
You can use docker to build and package the binary as a docker container

```
make build # builds the binary #output is in ./output/
make container # builds the container image (depends on REGISTRY and VERSION env vars)
```

# Deploy

## Deploy as Part of Control Plane
Use `./build-test-tools/deploy/static-manifest.yaml` as a static manifest (drop in `/etc/kubernetes/manifests/` directory) after you modify `--cloud-provider-init` argument

> More deployment samples TBD

## Run Locally (for dev/test inner loop)
./kubernetes-watchdog run 
	--logtostderr=true \
	-p azure \
	-i "client-id=<service principal client id>,client-password=<servce principal password>,tenant-id=<service principal tenant id>" -k <kubeconfig path> \
	--force-stagetwo-restart=true \ # if node stayed unready (but powered on) restart it
	--force-stagetwo-restart-wait=60s # restart an unready-powered-on after? 
```
 

# Contributing

This project welcomes contributions and suggestions.  Most contributions require you to agree to a
Contributor License Agreement (CLA) declaring that you have the right to, and actually do, grant us
the rights to use your contribution. For details, visit https://cla.microsoft.com.

When you submit a pull request, a CLA-bot will automatically determine whether you need to provide
a CLA and decorate the PR appropriately (e.g., label, comment). Simply follow the instructions
provided by the bot. You will only need to do this once across all repos using our CLA.

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/).
For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or
contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.
