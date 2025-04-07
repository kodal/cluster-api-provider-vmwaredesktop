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
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
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

	"github.com/kodal/vmrest-go-client"

	infrav1 "github.com/kodal/cluster-api-provider-vmware-desktop/api/v1alpha1"
)

// VDMachineReconciler reconciles a VDMachine object
type VDMachineReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=vdmachines,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=vdmachines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=infrastructure.cluster.x-k8s.io,resources=vdmachines/finalizers,verbs=update
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
	logger = logger.WithValues("providerID", vdMachine.Spec.ProviderID)

	// Fetch the Machine.
	machine, err := util.GetOwnerMachine(ctx, r.Client, vdMachine.ObjectMeta)
	if err != nil {
		return ctrl.Result{}, err
	}
	if machine == nil {
		logger.Info("Machine Controller has not yet set OwnerRef")
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

	// Handle deleted machines
	if !vdMachine.ObjectMeta.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, vdMachine)
	}

	// Fetch the Cluster.
	cluster, err := util.GetClusterFromMetadata(ctx, r.Client, machine.ObjectMeta)
	if err != nil {
		logger.Info("Machine is missing cluster label or cluster does not exist")
		return ctrl.Result{}, nil
	}

	vdCluster := &infrav1.VDCluster{}
	vdClusterNamespacedName := client.ObjectKey{
		Namespace: vdMachine.Namespace,
		Name:      cluster.Spec.InfrastructureRef.Name,
	}
	if err := r.Get(ctx, vdClusterNamespacedName, vdCluster); err != nil {
		logger.Info("VDCluster is not available yet")
		return ctrl.Result{}, nil
	}

	if annotations.IsPaused(cluster, vdMachine) {
		logger.Info("VDMachine or linked Cluster is marked as paused, not reconciling")
		return ctrl.Result{}, nil
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

	if r.GetID(vdMachine) == nil {
		clone := vmrest.VmCloneParameter{
			Name:     vdMachine.Name,
			ParentId: vdMachine.Spec.TemplateID,
		}

		logger.Info("Creating VM", "templateId", vdMachine.Spec.TemplateID)
		vm, _, err := client.VMManagementApi.CreateVM(ctx, clone, nil)
		if err != nil {
			return ctrl.Result{}, err
		}
		r.SetID(vdMachine, &vm.Id)
		logger.Info("VM created", "id", vm.Id)
		return ctrl.Result{}, nil
	}

	vmId := *r.GetID(vdMachine)

	logger = logger.WithValues("providerID", vmId)

	if !vdMachine.Status.Initialization.BootstrapDataProvided {

		logger.Info("Configuring VM")

		bootstrapData, err := GetSecretData(ctx, r.Client, vdMachine.ObjectMeta.Namespace, *machine.Spec.Bootstrap.DataSecretName)
		if err != nil {
			return ctrl.Result{}, err
		}

		logger.Info("Bootstrap data", "data", bootstrapData)

		bootstrapData = strings.ReplaceAll(bootstrapData, "{ provider_id }", *vdMachine.Spec.ProviderID)

		// Set guestinfo parameters using a map
		encodedBootstrapData := base64.StdEncoding.EncodeToString([]byte(bootstrapData))
		networkConfig := ""
		if vdMachine.Spec.NetworkConfig != nil {
			networkConfig = *vdMachine.Spec.NetworkConfig
		}
		metadata := fmt.Sprintf("instance-id: %s\nlocal-hostname: %s\n%s", *r.GetID(vdMachine), vdMachine.Name, networkConfig)
		encodedMetadata := base64.StdEncoding.EncodeToString([]byte(metadata))

		params := map[string]string{
			"guestinfo.userdata":          encodedBootstrapData,
			"guestinfo.userdata.encoding": "base64",
			"guestinfo.metadata":          encodedMetadata,
			"guestinfo.metadata.encoding": "base64",
		}

		for name, value := range params {
			configParam := vmrest.ConfigVmParamsParameter{
				Name:  name,
				Value: value,
			}
			_, _, err := client.VMManagementApi.ConfigVMParams(ctx, configParam, vmId, nil)
			if err != nil {
				return ctrl.Result{}, err
			}
		}

		vdMachine.Status.Initialization.BootstrapDataProvided = true
		logger.Info("VM configured with guestinfo parameters")
		return ctrl.Result{}, nil
	}

	vmState := vdMachine.Status.State
	if vmState == nil || *vmState == "poweringOn" {
		message := "Getting power state of VM"

		logger.Info(message)

		powerState, response, err := client.VMPowerManagementApi.GetPowerState(ctx, vmId, nil)
		if err != nil {
			if response.StatusCode == 404 {
				logger.Info("VM not found")
				vdMachine.Spec.ProviderID = nil
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, err
		}

		vdMachine.Status.State = &powerState.PowerState
		return ctrl.Result{}, nil
	}

	if *vdMachine.Status.State == "poweredOff" {
		message := "Powering on"

		logger.Info(message)

		powerState, response, err := client.VMPowerManagementApi.ChangePowerState(ctx, "on", vmId, nil)
		if err != nil {
			if response.StatusCode == 404 {
				logger.Info("VM not found")
				vdMachine.Spec.ProviderID = nil
				return ctrl.Result{}, nil
			}
			return ctrl.Result{}, err
		}

		vdMachine.Status.State = &powerState.PowerState
		return ctrl.Result{}, nil
	}

	if len(vdMachine.Status.Addresses) == 0 {
		message := "Getting IP address"

		logger.Info(message)

		ip, response, err := client.VMNetworkAdaptersManagementApi.GetIPAddress(ctx, vmId, nil)
		if err != nil {
			if response.StatusCode == 500 {
				return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
			}
			return ctrl.Result{}, err
		}

		logger.Info("VM got IP address", "ip", ip.Ip)
		vdMachine.Status.Addresses = []clusterv1.MachineAddress{
			{
				Type:    clusterv1.MachineInternalIP,
				Address: ip.Ip,
			},
		}

		// Set Ready to true.
		vdMachine.Status.Ready = true
		vdMachine.Status.Initialization.Provisioned = true
		return ctrl.Result{}, nil
	}

	return ctrl.Result{}, nil
}

func (r *VDMachineReconciler) reconcileDelete(ctx context.Context, vdMachine *infrav1.VDMachine) (_ ctrl.Result, rerr error) { // nolint:unparam
	logger := log.FromContext(ctx)

	vmId := r.GetID(vdMachine)
	if vmId == nil {
		logger.Info("VM is already deleted")
		controllerutil.RemoveFinalizer(vdMachine, infrav1.MachineFinalizer)
		return ctrl.Result{}, nil
	}

	client, ctx := NewVDClient(ctx)

	if *vdMachine.Status.State != "poweredOff" {
		logger.Info("Power of VM")
		powerState, response, err := client.VMPowerManagementApi.ChangePowerState(ctx, "off", *vmId, nil)
		if err != nil {
			if response.StatusCode == 404 {
				logger.Info("VM already deleted")
				controllerutil.RemoveFinalizer(vdMachine, infrav1.MachineFinalizer)

			}
			return ctrl.Result{}, err
		}

		powerState, response, err = client.VMPowerManagementApi.GetPowerState(ctx, *vmId, nil)
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
	_, err := client.VMManagementApi.DeleteVM(ctx, *vmId, nil)

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

func (r *VDMachineReconciler) GetID(m *infrav1.VDMachine) *string {
	if m.Spec.ProviderID == nil {
		return nil
	}
	providerID := *m.Spec.ProviderID
	id := providerID[len("vmware-desktop://"):]
	return &id
}

func (r *VDMachineReconciler) SetID(m *infrav1.VDMachine, id *string) {
	if id == nil {
		m.Spec.ProviderID = nil
		return
	}
	providerID := "vmware-desktop://" + *id
	m.Spec.ProviderID = &providerID
}
