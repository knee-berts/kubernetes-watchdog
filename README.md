* Status: Alpha *

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

CLI flags: 
```
  -p, --cloud-provider string                  cloud provider to use (default "azure")
  -i, --cloud-provider-init string             cloud provider free form init string
      --force-stagetwo-restart                 force a restart for  powered on not 'Ready' node)
  -f, --force-stagetwo-restart-wait duration   time waiting on a powered on not 'Ready' node before forcing restart) (default 5m0s)
  -h, --help                                   help for run
  -k, --kubeconfig string                      kubeconfig path. if empty in cluster config will be used
      --leader-election-id string              leader election id (should be unique within cluster) (default "kbuernetes-watchdog")
      --leader-election-name string            current leader name, 'hostname' is default (default "horus")
  -n, --leader-election-namespace string       namespace to create leader election object, 'default' namespace is default (default "default")
      --leader-election-ttl duration           leader election ttl (default 30s)
  -m, --max-node-processing-time duration      max allocated time for processing a single node (wraps all arm calls)) (default 2m0s)
  -s, --run-interval duration                  time between run. each run checks tracked node status check) (default 10s)
  -a, --stageone-wait duration                 time for a not 'Ready' node to go into checking power status stage) (default 30s)
  -c, --stagethree-wait duration               time waiting on a powered down not 'Ready' node before detaching disks) (default 30s)
  -b, --stagetwo-wait duration                 time waiting on a not 'Ready' node before checking power status) (default 30s)
  -r, --sync-interval duration                 controller cache sync (default 30s)
      --track-masters                          if enabled masters will be tracked for failures. (use it if masters has no data disks)
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
