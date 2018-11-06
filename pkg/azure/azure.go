package azure

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/golang/glog"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2018-06-01/compute"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	azureSDK "github.com/Azure/go-autorest/autorest/azure"

	"github.com/Azure/kubernetes-watchdog/pkg/watchdog"
)

const (
	errInvalidInitString  string = "invalid azure init string expected [x:y,a:b,n:i]"
	errInvalidInitKey     string = "invalid azure init string, unidentified key %s"
	errInvalidIdentityCfg string = "invalid azure identity configuration expected tenant-id and (client-id, client-password || use-msi || use-emsi, emsi)"
	errBoolValueExpected  string = "bool value (true/false) expected for %s"

	userAgent      string = "kubernetes-watchdog"
	keySPClientID  string = "client-id"
	keySPClientPwd string = "client-password"
	keyTenantID    string = "tenant-id"
	keyCloudName   string = "cloud-Name"
	keyuseMSI      string = "use-msi"
	keyuseEMSI     string = "use-e-msi"
	keyEMSI        string = "emsi"
)

type azureCloudHandler struct {
	client_id  string
	client_pwd string
	tenant_id  string
	cloudName  string
	useMSI     bool
	useEMSI    bool
	eMSI       string
	env        *azureSDK.Environment
}

func NewAzureCloudHandler(init string) (watchdog.CloudHandler, error) {
	glog.V(2).Infof("Azure cloud provider is initializing")
	var env *azureSDK.Environment
	var err error
	azure := &azureCloudHandler{}

	if 0 == len(init) {
		return nil, fmt.Errorf(errInvalidInitString)
	}

	split := strings.Split(init, ",")
	if 0 == len(split) {
		return nil, fmt.Errorf(errInvalidInitString)
	}

	for _, v := range split {
		entrySplit := strings.Split(v, ":")
		if 2 != len(entrySplit) {
			return nil, fmt.Errorf(errInvalidInitString)
		}
		switch entrySplit[0] {
		case keySPClientID:
			azure.client_id = entrySplit[1]
		case keySPClientPwd:
			azure.client_pwd = entrySplit[1]
		case keyCloudName:
			azure.cloudName = entrySplit[1]
		case keyTenantID:
			azure.tenant_id = entrySplit[1]
		case keyuseMSI:
			b, err := strconv.ParseBool(entrySplit[1])
			if nil != err {
				return nil, fmt.Errorf(errBoolValueExpected, keyuseMSI)
			}
			azure.useMSI = b
		case keyuseEMSI:
			b, err := strconv.ParseBool(entrySplit[1])
			if nil != err {
				return nil, fmt.Errorf(errBoolValueExpected, keyuseEMSI)
			}
			azure.useEMSI = b
		case keyEMSI:
			azure.eMSI = entrySplit[1]
		default:
			return nil, fmt.Errorf(errInvalidInitKey, entrySplit[0])
		}
	}

	// Set env
	if env, err = parseAzureCloudName(azure.cloudName); nil != err {
		return nil, err
	}
	azure.env = env

	// Validate
	if err = validateConfig(azure); nil != err {
		return nil, err
	}
	return azure, nil
}

func (azure *azureCloudHandler) IsNodePoweredDown(ctx context.Context, nodeName, nodeProviderId string) (bool, error) {
	isVMSS, subscriptionId, resourceGroup, _, instanceName := parseProviderId(nodeProviderId)
	if isVMSS {
		return false, fmt.Errorf("VMSS is not impleneted yet")
	} else {
		vmClient, err := azure.getVMClient(subscriptionId)
		if nil != err {
			return false, err
		}

		vm, err := vmClient.InstanceView(ctx, resourceGroup, instanceName)
		if nil != err {
			return false, err
		}
		// find in statuses "running"
		if nil != vm.Statuses {
			for _, v := range *(vm.Statuses) {
				if nil != v.Code && "PowerState/running" == *(v.Code) {
					return false, nil
				}
			}
		}
		return true, nil
	}
}

func (azure *azureCloudHandler) DetachDisks(ctx context.Context, nodeName, nodeProviderId string) error {
	isVMSS, subscriptionId, resourceGroup, _, instanceName := parseProviderId(nodeProviderId)
	if isVMSS {
		return fmt.Errorf("VMSS is not implemented yet")
	} else {
		vmClient, err := azure.getVMClient(subscriptionId)
		if nil != err {
			return err
		}

		existingVM, err := vmClient.Get(ctx, resourceGroup, instanceName, compute.InstanceView)
		if nil != err {
			return err
		}

		if nil != existingVM.StorageProfile.DataDisks && 0 == len(*(existingVM.StorageProfile.DataDisks)) {
			glog.Infof("Request for detaching data disks from Node:%s, node has no data disks, ignoring", nodeName)
		} else {
			glog.Infof("node: %s has %d disks, they will be detached", nodeName, len(*existingVM.StorageProfile.DataDisks))

			disks := make([]compute.DataDisk, 0)
			updatedVM := compute.VirtualMachine{
				Location: existingVM.Location,
				VirtualMachineProperties: &compute.VirtualMachineProperties{
					StorageProfile: &compute.StorageProfile{
						DataDisks: &disks,
					},
				},
			}

			future, err := vmClient.CreateOrUpdate(ctx, resourceGroup, instanceName, updatedVM)
			if nil != err {
				return err
			}

			err = future.WaitForCompletion(ctx, vmClient.Client)
			if nil != err {
				return err
			}
		}
		return nil
	}
}

func (azure *azureCloudHandler) RestartNode(ctx context.Context, nodeName, nodeProviderId string) error {
	isVMSS, subscriptionId, resourceGroup, _, instanceName := parseProviderId(nodeProviderId)
	if isVMSS {
		return fmt.Errorf("VMSS is not implemented yet")
	} else {
		vmClient, err := azure.getVMClient(subscriptionId)
		if nil != err {
			return err
		}
		future, err := vmClient.Restart(ctx, resourceGroup, instanceName)
		if nil != err {
			return err
		}

		if err := future.WaitForCompletion(ctx, vmClient.Client); nil != err {
			return err
		}
		return nil
	}
}

func (azure *azureCloudHandler) getSPToken() (*adal.ServicePrincipalToken, error) {
	if azure.useMSI || azure.useEMSI {

		msiEndpoint, err := adal.GetMSIVMEndpoint()
		if err != nil {
			return nil, fmt.Errorf("Getting the managed service identity endpoint: %v", err)
		}
		if azure.useMSI {
			glog.Infoln("azure: using managed identity extension to retrieve access token")
			return adal.NewServicePrincipalTokenFromMSI(msiEndpoint, azure.env.ServiceManagementEndpoint)
		}
		glog.Info("azure: using User Assigned MSI ID to retrieve access token")
		return adal.NewServicePrincipalTokenFromMSIWithUserAssignedID(msiEndpoint, azure.env.ServiceManagementEndpoint, azure.eMSI)
	}

	oauthConfig, err := adal.NewOAuthConfig(azure.env.ActiveDirectoryEndpoint, azure.tenant_id)
	if err != nil {
		return nil, fmt.Errorf("creating the OAuth config: %v", err)
	}

	return adal.NewServicePrincipalToken(*oauthConfig, azure.client_id, azure.client_pwd, azure.env.ServiceManagementEndpoint)

}

func (azure *azureCloudHandler) getVMClient(subscriptionId string) (compute.VirtualMachinesClient, error) {
	vmClient := compute.NewVirtualMachinesClient(subscriptionId)
	token, err := azure.getSPToken()
	if nil != err {
		return compute.VirtualMachinesClient{}, err
	}

	vmClient.Authorizer = autorest.NewBearerAuthorizer(token)
	vmClient.AddToUserAgent(userAgent)
	return vmClient, nil
}
func parseProviderId(providerId string) (isVMSS bool, subscriptionId, resourceGroup, vmssName, instanceName string) {
	//TODO handle VMSS case
	// VMAS looks like this azure:///subscriptions/<SUB-ID>/resourceGroups/<RG-NAME>/providers/Microsoft.Compute/virtualMachines/<VM-NAME>
	new_id := providerId[len("azure:///"):]

	split := strings.Split(new_id, "/")

	isVMSS = false
	subscriptionId = split[1]
	resourceGroup = split[3]
	vmssName = ""
	instanceName = split[7]

	return
}

func parseAzureCloudName(cloudName string) (*azureSDK.Environment, error) {
	var env azureSDK.Environment
	var err error
	if cloudName == "" {
		env = azureSDK.PublicCloud
		glog.Infof("Azure cloud provider is using %s as cloud name", "PublicCloud")
	} else {
		env, err = azureSDK.EnvironmentFromName(cloudName)
		glog.Infof("Azure cloud provider is using %s as cloud name", cloudName)
	}
	return &env, err
}

func validateConfig(azure *azureCloudHandler) error {
	if 0 == len(azure.tenant_id) {
		return fmt.Errorf(errInvalidIdentityCfg)
	}

	if azure.useEMSI && 0 == len(azure.eMSI) {
		return fmt.Errorf(errInvalidIdentityCfg)
	}

	if !azure.useMSI && (0 == len(azure.client_id) || 0 == len(azure.client_pwd)) {
		return fmt.Errorf(errInvalidIdentityCfg)
	}

	return nil
}
