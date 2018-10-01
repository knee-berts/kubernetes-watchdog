*warning this is an early alpha project, DO NOT use in production*


Kubernetes watchdog:
1. Watches for not `Ready` nodes and attempts to restart them
2. Watches for not `Ready` + `Powered Off` nodes and detaches disks to allow kubernetes to move disks to other nodes


Build: 

```
#clone repo 
git clone https://github.com/Azure/kubernetes-watchdog.git

# build 
go build .

# run
./kubernetes-watchdog run --logtostderr=true  -p azure -i "client-id=f2814944-4777-44ee-9917-4ab1f68b814a,client-password=Super9Complex9Password,tenant-id=72f988bf-86f1-41af-91ab-2d7cd011db47" -k ~/tmp/cairo/kubeconfig --force-stagetwo-restart=true --force-stagetwo-restart-wait=60s
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
