/*
Copyright 2025.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ipamv1 "sigs.k8s.io/cluster-api/exp/ipam/api/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	corev1 "k8s.io/api/core/v1"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/cluster-api/util"
	"sigs.k8s.io/cluster-api/util/annotations"
	"sigs.k8s.io/cluster-api/util/patch"

	"github.com/go-logr/logr"
	"github.com/kodal/vmrest-go-client"

	infrav1 "github.com/kodal/cluster-api-provider-vmwaredesktop/api/v1alpha1"
)

// VDMachineReconciler reconciles a VDMachine object
type VDMachineReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

type Metadata struct {
	InstanceId *string
	Hostname   *string
	NodeIP     *string
	Ethernets  []Ethernet
}

type Ethernet struct {
	Name        *string
	Dhcp4       *bool
	Dhcp6       *bool
	Gateway4    *string
	Gateway6    *string
	Addresses   []string
	Nameservers []string
	Routes      []Route
}

type Route struct {
	To  string
	Via *string
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=vdmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=vdmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=vdmachines/finalizers,verbs=update
// +kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddressclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ipam.cluster.x-k8s.io,resources=ipaddresses,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=clusters;machines,verbs=get;list;watch
// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines;machines/status,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=secrets;,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the VDMachine object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.20.2/pkg/reconcile
func (r *VDMachineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (_ ctrl.Result, rerr error) {

	logger := log.FromContext(ctx)

	vdMachine := &infrav1.VDMachine{}
	if err := r.Get(ctx, req.NamespacedName, vdMachine); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	logger = logger.WithValues("vmID", vdMachine.Spec.VmID)

	// Fetch the Machine.
	machine, err := util.GetOwnerMachine(ctx, r.Client, vdMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if machine == nil {
		logger.Info("Machine Controller has not yet set OwnerRef")
		return ctrl.Result{}, nil
	}

	// Fetch the Cluster.
	cluster, err := util.GetClusterFromMetadata(ctx, r.Client, machine.ObjectMeta)
	if err != nil {
		logger.Info("Machine is missing cluster label or cluster does not exist")
		return ctrl.Result{}, nil
	}

	if annotations.IsPaused(cluster, vdMachine) {
		logger.Info("VDMachine or linked Cluster is marked as paused, not reconciling")
		return ctrl.Result{}, nil
	}

	// patch from sigs.k8s.io/cluster-api/util/patch
	helper, err := patch.NewHelper(vdMachine, r.Client)
	if err != nil {
		return ctrl.Result{}, err
	}
	defer func() {
		if err := helper.Patch(ctx, vdMachine); err != nil {
			logger.Error(err, "Failed to patch VDMachine")
			if rerr == nil {
				rerr = err
			}
		}
	}()

	vdClient, ctx := NewVDClient(ctx)
	vmId := vdMachine.Spec.VmID
	if vmId != nil {
		powerState, response, err := vdClient.VMPowerManagementAPI.GetPowerState(ctx, *vmId).Execute()
		if err != nil {
			if response.StatusCode == 404 {
				logger.Info("VM not found")
				vdMachine.Spec.ProviderID = nil
				vdMachine.Spec.VmID = nil
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, err
		}

		vdMachine.Status.State = &powerState.PowerState
		if err := helper.Patch(ctx, vdMachine); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Handle deleted machines
	if !vdMachine.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, vdMachine)
	}

	return r.reconcileNormal(ctx, cluster, machine, vdMachine)
}

func (r *VDMachineReconciler) reconcileNormal(ctx context.Context, cluster *clusterv1.Cluster, machine *clusterv1.Machine, vdMachine *infrav1.VDMachine) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Add finalizer to the VDMachine.
	controllerutil.AddFinalizer(vdMachine, infrav1.MachineFinalizer)

	// Check if the infrastructure is ready, otherwise return and wait for the cluster object to be updated
	if !cluster.Status.InfrastructureReady {
		logger.Info("Waiting for VDCluster Controller to create cluster infrastructure")
		return ctrl.Result{}, nil
	}

	// Make sure bootstrap data is available and populated.
	if machine.Spec.Bootstrap.DataSecretName == nil {
		logger.Info("Bootstrap data secret reference is not yet available")
		return ctrl.Result{}, nil
	}

	client, ctx := NewVDClient(ctx)

	vmId := vdMachine.Spec.VmID

	logger = logger.WithValues("vmID", vmId)

	if vmId == nil {
		clone := vmrest.VMCloneParameter{
			Name:     vdMachine.Name,
			ParentId: vdMachine.Spec.TemplateID,
		}

		logger.Info("Creating VM", "templateId", vdMachine.Spec.TemplateID)
		vm, _, err := client.VMManagementAPI.CreateVM(ctx).VMCloneParameter(clone).Execute()
		if err != nil {
			r.logErrorResponse(err, logger)
			return ctrl.Result{}, fmt.Errorf("failed to create VM: %v", err)
		}
		providerID := fmt.Sprintf("vmwaredesktop://%s", vm.Id)
		vdMachine.Spec.ProviderID = &providerID
		vdMachine.Spec.VmID = &vm.Id

		hardware := infrav1.VDHardware{
			Cpu:    *vm.Cpu.Processors,
			Memory: *vm.Memory,
		}
		vdMachine.Status.Hardware = hardware
		logger.Info("VM created", "id", vm.Id)
		return ctrl.Result{}, nil
	}

	cpuConfigured := vdMachine.Spec.Cpu == nil || *vdMachine.Spec.Cpu == vdMachine.Status.Hardware.Cpu
	memoryConfigured := vdMachine.Spec.Memory == nil || *vdMachine.Spec.Memory == vdMachine.Status.Hardware.Memory

	if !cpuConfigured || !memoryConfigured {

		cpu := vdMachine.Status.Hardware.Cpu
		memory := vdMachine.Status.Hardware.Memory
		if vdMachine.Spec.Cpu != nil {
			cpu = *vdMachine.Spec.Cpu
		}
		if vdMachine.Spec.Memory != nil {
			memory = *vdMachine.Spec.Memory
		}

		logger.Info("Updating VM hardware", "cpu", cpu, "memory", memory)

		vdParameter := vmrest.VMParameter{
			Processors: &cpu,
			Memory:     &memory,
		}

		result, _, err := client.VMManagementAPI.UpdateVM(ctx, *vmId).VMParameter(vdParameter).Execute()
		if err != nil {
			r.logErrorResponse(err, logger)
			return ctrl.Result{}, fmt.Errorf("failed to update VM hardware: %v", err)
		}
		logger.Info("VM hardware updated", "result", result)
		hardware := infrav1.VDHardware{
			Cpu:    *result.Cpu.Processors,
			Memory: *result.Memory,
		}
		vdMachine.Status.Hardware = hardware
		return ctrl.Result{}, nil
	}

	if res, err := r.reconcileNetworkAdapters(ctx, client, vmId, vdMachine, logger); err != nil || !res.IsZero() {
		return res, err
	}

	if res, err := r.reconcileNetworkAddresses(ctx, vdMachine, logger); err != nil || !res.IsZero() {
		return res, err
	}
	if res, err := r.reconcileSharedFolders(ctx, client, vmId, vdMachine, logger); err != nil || !res.IsZero() {
		return res, err
	}

	if err := r.reconcileBootstrapData(ctx, client, vmId, machine, vdMachine, logger); err != nil {
		return ctrl.Result{}, err
	}

	if *vdMachine.Status.State == "poweredOff" {
		message := "Powering on"

		logger.Info(message)

		powerState, response, err := client.VMPowerManagementAPI.ChangePowerState(ctx, *vmId).Body(string(vmrest.VMPOWEROPERATION_ON)).Execute()
		if err != nil {
			if response.StatusCode == 404 {
				logger.Info("VM not found")
				vdMachine.Spec.ProviderID = nil
				return ctrl.Result{}, nil
			}
			r.logErrorResponse(err, logger)
			return ctrl.Result{}, fmt.Errorf("failed to power on VM: %v", err)
		}

		vdMachine.Status.State = &powerState.PowerState
		return ctrl.Result{}, nil
	}

	if len(vdMachine.Status.Addresses) == 0 {
		logger.Info("Getting IP address")

		nics, response, err := client.VMNetworkAdaptersManagementAPI.GetNicInfo(ctx, *vmId).Execute()
		if err != nil {
			if response.StatusCode == 500 {
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}

		if len(nics.Nics) == 0 {
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}

		addresses := []clusterv1.MachineAddress{}

		for i, nic := range nics.Nics {
			var typeIP *clusterv1.MachineAddressType
			if i < len(vdMachine.Spec.Network.Ethernets) {
				typeIP = vdMachine.Spec.Network.Ethernets[i].TypeIP
			}
			if typeIP == nil {
				internalIP := clusterv1.MachineInternalIP
				typeIP = &internalIP
			}
			for _, ip := range nic.Ip {
				addresses = append(addresses, clusterv1.MachineAddress{
					Type:    *typeIP,
					Address: ip,
				})
			}
		}
		logger.Info("VM got IP addresses", "IPs", addresses)
		vdMachine.Status.Addresses = addresses

		// Set Ready to true.
		vdMachine.Status.Ready = true
		vdMachine.Status.Initialization.Provisioned = true
		logger.Info("VDMachine is ready")
		return ctrl.Result{}, nil
	}

	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *VDMachineReconciler) reconcileBootstrapData(
	ctx context.Context,
	client *vmrest.APIClient,
	vmId *string,
	machine *clusterv1.Machine,
	vdMachine *infrav1.VDMachine,
	logger logr.Logger,
) error {
	if vdMachine.Status.Initialization.BootstrapDataProvided {
		return nil
	}

	logger.Info("Configuring VM")

	bootstrapData, err := GetSecretData(ctx, r.Client, vdMachine.Namespace, *machine.Spec.Bootstrap.DataSecretName)
	if err != nil {
		return err
	}

	logger.Info("Bootstrap data", "data", bootstrapData)

	bootstrapData = strings.ReplaceAll(bootstrapData, "{ provider_id }", *vdMachine.Spec.ProviderID)
	encodedBootstrapData := base64.StdEncoding.EncodeToString([]byte(bootstrapData))

	ethernets := []Ethernet{}
	var nodeIP *string

	for _, vdNetworkEthernet := range vdMachine.Spec.Network.Ethernets {

		ipamAddress, err := r.findIpamAddress(ctx, vdMachine, &vdNetworkEthernet)
		if err != nil {
			return err
		}
		var addresses []string
		var routes []Route
		if ipamAddress != nil && ipamAddress.Spec.Address != "" {
			addresses = append(addresses, fmt.Sprintf("%s/%d", ipamAddress.Spec.Address, ipamAddress.Spec.Prefix))
			if vdNetworkEthernet.IpamAsNodeIP != nil && *vdNetworkEthernet.IpamAsNodeIP {
				nodeIP = &ipamAddress.Spec.Address
			}
		}
		for _, r := range vdNetworkEthernet.Routes {
			newRoute := Route {
				To: r.To,
				Via: r.Via,
			}
			if newRoute.Via == nil && ipamAddress != nil {
				newRoute.Via = &ipamAddress.Spec.Gateway
			}
			routes = append(routes, newRoute)
		}
		ethernets = append(ethernets, Ethernet{
			Name:        &vdNetworkEthernet.Name,
			Dhcp4:       vdNetworkEthernet.Dhcp4,
			Dhcp6:       vdNetworkEthernet.Dhcp6,
			Addresses:   addresses,
			Nameservers: vdNetworkEthernet.Nameservers,
			Routes:      routes,
		})
	}

	metadata := Metadata{
		InstanceId: vmId,
		Hostname:   &vdMachine.Name,
		NodeIP:     nodeIP,
		Ethernets:  ethernets,
	}
	metadataTemplater, err := template.New("metadata").Parse(metadataTemplate)
	if err != nil {
		return err
	}
	buffer := &bytes.Buffer{}
	if err = metadataTemplater.Execute(buffer, metadata); err != nil {
		return fmt.Errorf("failed to render %s", err)
	}
	metadataString := buffer.String()

	encodedMetadata := base64.StdEncoding.EncodeToString([]byte(metadataString))
	logger.Info("metadata", "metadata", metadataString)

	params := map[string]string{
		"guestinfo.userdata":          encodedBootstrapData,
		"guestinfo.userdata.encoding": "base64",
		"guestinfo.metadata":          encodedMetadata,
		"guestinfo.metadata.encoding": "base64",
	}

	for name, value := range params {
		configParam := vmrest.ConfigVMParamsParameter{
			Name:  &name,
			Value: &value,
		}
		_, _, err := client.VMManagementAPI.ConfigVMParams(ctx, *vmId).ConfigVMParamsParameter(configParam).Execute()
		if err != nil {
			return err
		}
	}

	vdMachine.Status.Initialization.BootstrapDataProvided = true
	logger.Info("VM configured with guestinfo parameters")

	return nil
}

func (r *VDMachineReconciler) reconcileNetworkAdapters(
	ctx context.Context,
	client *vmrest.APIClient,
	vmId *string,
	vdMachine *infrav1.VDMachine,
	logger logr.Logger,
) (ctrl.Result, error) {

	hasNetwork := vdMachine.Spec.Network != nil
	hasAdapters := hasNetwork && vdMachine.Spec.Network.Adapters != nil

	if !hasAdapters && vdMachine.Status.NetworkAdapters != nil {
		return ctrl.Result{}, nil
	}

	if hasAdapters {
		containsAll := true
		for _, adapter := range vdMachine.Spec.Network.Adapters {
			if !contains(vdMachine.Status.NetworkAdapters, adapter) {
				containsAll = false
				break
			}
		}

		if containsAll {
			return ctrl.Result{}, nil
		}
	}

	logger.Info("reconcile network adapters")

	resultNetworkAdapters, _, err := client.VMNetworkAdaptersManagementAPI.GetAllNICDevices(ctx, *vmId).Execute()
	if err != nil {
		r.logErrorResponse(err, logger)
		return ctrl.Result{}, fmt.Errorf("failed to get VM network adapters: %v", err)
	}

	statusNetworkAdapters := []infrav1.VDNetworkAdapter{}
	for _, networkAdapter := range resultNetworkAdapters.Nics {
		statusNetworkAdapters = append(statusNetworkAdapters, infrav1.VDNetworkAdapter{
			Type:  &networkAdapter.Type,
			Vmnet: &networkAdapter.Vmnet,
		})
	}

	vdMachine.Status.NetworkAdapters = statusNetworkAdapters

	if !hasAdapters {
		return ctrl.Result{}, nil
	}

	for _, adapter := range vdMachine.Spec.Network.Adapters {
		if !contains(vdMachine.Status.NetworkAdapters, adapter) {
			nicParams := vmrest.NICDeviceParameter{
				Type:  *adapter.Type,
				Vmnet: *adapter.Vmnet,
			}
			nic, _, err := client.VMNetworkAdaptersManagementAPI.CreateNICDevice(ctx, *vmId).NICDeviceParameter(nicParams).Execute()
			if err != nil {
				r.logErrorResponse(err, logger)
				return ctrl.Result{}, fmt.Errorf("failed to create network adapter: %v", err)
			}

			name := fmt.Sprintf("ethernet%d.virtualDev", nic.Index-1)
			value := "e1000e"
			configParams := vmrest.ConfigVMParamsParameter{
				Name:  &name,
				Value: &value,
			}
			_, _, err = client.VMManagementAPI.ConfigVMParams(ctx, *vmId).ConfigVMParamsParameter(configParams).Execute()
			if err != nil {
				r.logErrorResponse(err, logger)
				return ctrl.Result{}, fmt.Errorf("failed to create network adapter: %v", err)
			}
		}
	}

	logger.Info("successfully reconciled network adapters")

	return ctrl.Result{RequeueAfter: time.Millisecond}, nil
}

func (r *VDMachineReconciler) reconcileNetworkAddresses(
	ctx context.Context,
	vdMachine *infrav1.VDMachine,
	logger logr.Logger,
) (ctrl.Result, error) {
	if vdMachine.Spec.Network == nil || vdMachine.Spec.Network.Ethernets == nil || len(vdMachine.Spec.Network.Ethernets) == 0 {
		return ctrl.Result{}, nil
	}
	for _, ethernet := range vdMachine.Spec.Network.Ethernets {
		if ethernet.IpamRef != nil {
			claimName := vdMachine.Name + "-" + ethernet.Name
			claim := &ipamv1.IPAddressClaim{}
			err := r.Get(ctx, client.ObjectKey{
				Name: claimName, Namespace: vdMachine.Namespace,
			}, claim)

			if apierrors.IsNotFound(err) {
				claim = &ipamv1.IPAddressClaim{
					ObjectMeta: metav1.ObjectMeta{
						Name:      claimName,
						Namespace: vdMachine.Namespace,
						OwnerReferences: []metav1.OwnerReference{
							*metav1.NewControllerRef(vdMachine, infrav1.GroupVersion.WithKind("VDMachine")),
						},
					},
					Spec: ipamv1.IPAddressClaimSpec{
						PoolRef: *ethernet.IpamRef,
					},
				}
				logger.Info("creating ip claim", "claim", claim)
				err = r.Create(ctx, claim)
				if err != nil {
					return ctrl.Result{}, err
				}
			}

			if err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, fmt.Errorf("failed to get ipclaim %s", err)
			}

			address := &ipamv1.IPAddress{}
			err = r.Get(ctx, client.ObjectKey{
				Name: claim.Name, Namespace: vdMachine.Namespace,
			}, address)
			if err != nil && !apierrors.IsNotFound(err) {
				return ctrl.Result{}, err
			}
			if address.Spec.Address == "" {
				logger.Info("waiting IP from IP Claim")
				return ctrl.Result{RequeueAfter: time.Second}, nil
			}
		}
	}
	return ctrl.Result{}, nil
}

func (r *VDMachineReconciler) findIpamAddress(
	ctx context.Context,
	vdMachine *infrav1.VDMachine,
	ethernet *infrav1.VDNetworkEthernet,
) (*ipamv1.IPAddress, error) {
	if vdMachine == nil || ethernet == nil {
		return nil, fmt.Errorf("failed to get ipam address")
	}
	claimName := vdMachine.Name + "-" + ethernet.Name
	address := &ipamv1.IPAddress{}
	err := r.Get(ctx, client.ObjectKey{
		Name: claimName, Namespace: vdMachine.Namespace,
	}, address)
	if apierrors.IsNotFound(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return address, nil
}

func (r *VDMachineReconciler) reconcileSharedFolders(
	ctx context.Context,
	client *vmrest.APIClient,
	vmId *string,
	vdMachine *infrav1.VDMachine,
	logger logr.Logger,
) (ctrl.Result, error) {

	if sharedFoldersEqual(vdMachine.Spec.SharedFolders, vdMachine.Status.SharedFolders) {
		return ctrl.Result{}, nil
	}

	logger.Info("reconcile shared folders")

	resultSharedFolders, _, err := client.VMSharedFoldersManagementAPI.GetAllSharedFolders(ctx, *vmId).Execute()
	if err != nil {
		r.logErrorResponse(err, logger)
		return ctrl.Result{}, fmt.Errorf("failed to get VM shared folders: %v", err)
	}
	statusSharedFolders := []infrav1.VDSharedFolder{}
	for _, sharedFolder := range resultSharedFolders {
		statusSharedFolders = append(statusSharedFolders, infrav1.VDSharedFolder{
			FolderId: sharedFolder.FolderId,
			HostPath: sharedFolder.HostPath,
			Flags:    &sharedFolder.Flags,
		})
	}
	vdMachine.Status.SharedFolders = statusSharedFolders

	if sharedFoldersEqual(vdMachine.Spec.SharedFolders, vdMachine.Status.SharedFolders) {
		logger.Info("spec shared folders & status shared folders are equal")
		return ctrl.Result{}, nil
	}

	specMap := make(map[string]infrav1.VDSharedFolder)
	for _, sf := range vdMachine.Spec.SharedFolders {
		specMap[sf.FolderId] = sf
	}

	statusMap := make(map[string]infrav1.VDSharedFolder)
	for _, sf := range vdMachine.Status.SharedFolders {
		statusMap[sf.FolderId] = sf
	}

	for id := range statusMap {
		if _, exists := specMap[id]; !exists {
			logger.Info("Deleting stale shared folder", "FolderId", id)
			_, err := client.VMSharedFoldersManagementAPI.DeleteSharedFolder(ctx, *vmId, id).Execute()
			if err != nil {
				r.logErrorResponse(err, logger)
				return ctrl.Result{}, fmt.Errorf("failed to delete stale shared folder %q: %v", id, err)
			}
		}
	}

	for _, sf := range vdMachine.Spec.SharedFolders {
		flags := int32(4)
		if sf.Flags != nil {
			flags = *sf.Flags
		}

		params := vmrest.SharedFolder{
			FolderId: sf.FolderId,
			HostPath: sf.HostPath,
			Flags:    flags,
		}

		logger.Info("Creating/updating shared folder", "FolderId", sf.FolderId, "HostPath", sf.HostPath)
		_, _, err := client.VMSharedFoldersManagementAPI.CreateSharedFolder(ctx, *vmId).SharedFolder(params).Execute()
		if err != nil {
			r.logErrorResponse(err, logger)
			return ctrl.Result{}, fmt.Errorf("failed to create shared folder %q: %v", sf.FolderId, err)
		}
	}

	logger.Info("shared folders successfully configured")

	return ctrl.Result{RequeueAfter: time.Millisecond}, nil
}

func contains(adapters []infrav1.VDNetworkAdapter, adapter infrav1.VDNetworkAdapter) bool {
	return slices.ContainsFunc(adapters, func(a infrav1.VDNetworkAdapter) bool {
		return *a.Type == *adapter.Type && *a.Vmnet == *adapter.Vmnet
	})
}

func sharedFoldersEqual(spec []infrav1.VDSharedFolder, status []infrav1.VDSharedFolder) bool {
	specMap := make(map[string]infrav1.VDSharedFolder)
	for _, sf := range spec {
		specMap[sf.FolderId] = sf
	}

	statusMap := make(map[string]infrav1.VDSharedFolder)
	for _, sf := range status {
		statusMap[sf.FolderId] = sf
	}

	if len(specMap) != len(statusMap) {
		return false
	}

	for id, specFolder := range specMap {
		statusFolder, ok := statusMap[id]
		if !ok {
			return false
		}

		if specFolder.HostPath != statusFolder.HostPath {
			return false
		}
	}
	return true
}

func (r *VDMachineReconciler) logErrorResponse(err error, logger logr.Logger) {
	if swaggerErr, ok := err.(vmrest.GenericOpenAPIError); ok {
		logger.Info("Response error", "errorModel", swaggerErr.Model())
	}

}

func (r *VDMachineReconciler) reconcileDelete(ctx context.Context, vdMachine *infrav1.VDMachine) (_ ctrl.Result, rerr error) { // nolint:unparam
	logger := log.FromContext(ctx)

	vmId := vdMachine.Spec.VmID
	if vmId == nil {
		logger.Info("VM is already deleted")
		controllerutil.RemoveFinalizer(vdMachine, infrav1.MachineFinalizer)
		return ctrl.Result{}, nil
	}

	client, ctx := NewVDClient(ctx)

	if *vdMachine.Status.State != "poweredOff" {
		logger.Info("Power of VM")
		_, response, err := client.VMPowerManagementAPI.ChangePowerState(ctx, *vmId).Body(string(vmrest.VMPOWEROPERATION_OFF)).Execute()
		if err != nil {
			if response.StatusCode == 404 {
				logger.Info("VM already deleted")
				controllerutil.RemoveFinalizer(vdMachine, infrav1.MachineFinalizer)

			}
			return ctrl.Result{}, err
		}

		powerState, response, err := client.VMPowerManagementAPI.GetPowerState(ctx, *vmId).Execute()
		if err != nil {
			if response.StatusCode == 404 {
				logger.Info("VM already deleted")
				controllerutil.RemoveFinalizer(vdMachine, infrav1.MachineFinalizer)
			}
			return ctrl.Result{}, err
		}
		logger.Info("Power state", "state", powerState)
		vdMachine.Status.State = &powerState.PowerState
		return ctrl.Result{}, nil
	}

	logger.Info("Delete VM")
	_, err := client.VMManagementAPI.DeleteVM(ctx, *vmId).Execute()

	if err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("VM deleted")
	controllerutil.RemoveFinalizer(vdMachine, infrav1.MachineFinalizer)
	return ctrl.Result{}, nil
}

func GetSecretData(ctx context.Context, c client.Client, namespace, name string) (string, error) {
	secret := &corev1.Secret{}
	if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, secret); err != nil {
		return "", err
	}

	data, ok := secret.Data["value"]
	if !ok {
		return "", errors.New("secret does not contain value")
	}

	return string(data), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *VDMachineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&infrav1.VDMachine{}).
		Watches(
			&clusterv1.Machine{},
			handler.EnqueueRequestsFromMapFunc(util.MachineToInfrastructureMapFunc(infrav1.GroupVersion.WithKind("VDMachine"))),
		).
		Named("vdmachine").
		Complete(r)
}
