// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers

import (
	"context"
	"fmt"

	"github.com/ironcore-dev/controller-utils/clientutils"
	commonv1alpha1 "github.com/ironcore-dev/ironcore/api/common/v1alpha1"
	computev1alpha1 "github.com/ironcore-dev/ironcore/api/compute/v1alpha1"
	"github.com/ironcore-dev/pool-lifecycle-controller/internal/client/index"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	maintenanceTaintKey    = "maintenance.ironcore.dev"
	preDrainHookAnnotation = clusterv1.PreDrainDeleteHookAnnotationPrefix + "/ironcore-maintenance"
	preDrainHookValue      = "ironcore-maintenance"
	machinePoolFinalizer   = "maintenance.ironcore.dev/machinepool-cleanup"
)

type MachinePoolLifecycleReconciler struct {
	client.Client
	IronCoreClient client.Client

	CAPIMachineSelector labels.Selector
}

// +kubebuilder:rbac:groups=cluster.x-k8s.io,resources=machines,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=compute.ironcore.dev,resources=machinepools,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=compute.ironcore.dev,resources=machines,verbs=get;list;watch

// Reconcile reconciles the desired with the actual state.
func (r *MachinePoolLifecycleReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	capiMachine := &clusterv1.Machine{}
	if err := r.Get(ctx, req.NamespacedName, capiMachine); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !r.matchesSelector(capiMachine) {
		log.V(1).Info("Ignoring CAPI Machine, does not match configured selector", "machine", client.ObjectKeyFromObject(capiMachine))
		return ctrl.Result{}, nil
	}

	return r.reconcileExists(ctx, capiMachine)
}

func (r *MachinePoolLifecycleReconciler) reconcileExists(ctx context.Context, capiMachine *clusterv1.Machine) (ctrl.Result, error) {
	if !capiMachine.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, capiMachine)
	}
	return r.reconcile(ctx, capiMachine)
}

func (r *MachinePoolLifecycleReconciler) reconcile(ctx context.Context, capiMachine *clusterv1.Machine) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	modified, err := clientutils.PatchEnsureFinalizer(ctx, r.Client, capiMachine, machinePoolFinalizer)
	if err != nil {
		return ctrl.Result{}, err
	}
	if modified {
		log.V(1).Info("Added finalizer")
		return ctrl.Result{}, nil
	}

	if capiMachine.Annotations[preDrainHookAnnotation] == preDrainHookValue {
		return ctrl.Result{}, nil
	}

	base := client.MergeFrom(capiMachine.DeepCopy())
	if capiMachine.Annotations == nil {
		capiMachine.Annotations = map[string]string{}
	}
	capiMachine.Annotations[preDrainHookAnnotation] = preDrainHookValue
	if err := r.Patch(ctx, capiMachine, base); err != nil {
		return ctrl.Result{}, fmt.Errorf("error adding pre-drain hook: %w", err)
	}
	log.V(1).Info("Added pre-drain hook")

	return ctrl.Result{}, nil
}

func (r *MachinePoolLifecycleReconciler) reconcileDelete(ctx context.Context, capiMachine *clusterv1.Machine) (ctrl.Result, error) {
	log := ctrl.LoggerFrom(ctx)

	if capiMachine.Status.NodeRef.IsDefined() {
		poolName := capiMachine.Status.NodeRef.Name

		evacuated, err := r.evictMachinePool(ctx, poolName)
		if err != nil {
			return ctrl.Result{}, err
		}
		if !evacuated {
			log.V(1).Info("MachinePool still has non-tolerating machines, holding pre-drain hook", "machinePool", poolName)
			return ctrl.Result{}, nil
		}

		if err := r.removePreDrainHook(ctx, capiMachine); err != nil {
			return ctrl.Result{}, err
		}

		log.V(1).Info("MachinePool evacuated, releasing pre-drain hook", "machinePool", poolName)
		if controllerutil.ContainsFinalizer(capiMachine, clusterv1.MachineFinalizer) {
			log.V(1).Info("Waiting for CAPI to drain and delete the node", "machinePool", poolName)
			return ctrl.Result{}, nil
		}

		log.V(1).Info("Ensure machine pool is deleted")
		if err := r.deleteMachinePool(ctx, poolName); err != nil {
			return ctrl.Result{}, err
		}
	} else {
		log.V(1).Info("No node reference is defined, skipping cleanup")
		if err := r.removePreDrainHook(ctx, capiMachine); err != nil {
			return ctrl.Result{}, err
		}
	}

	if _, err := clientutils.PatchEnsureNoFinalizer(ctx, r.Client, capiMachine, machinePoolFinalizer); client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, fmt.Errorf("error removing finalizer: %w", err)
	}
	return ctrl.Result{}, nil
}

func (r *MachinePoolLifecycleReconciler) matchesSelector(capiMachine *clusterv1.Machine) bool {
	if r.CAPIMachineSelector != nil && !r.CAPIMachineSelector.Matches(labels.Set(capiMachine.GetLabels())) {
		return false
	}

	return true
}

func (r *MachinePoolLifecycleReconciler) removePreDrainHook(ctx context.Context, capiMachine *clusterv1.Machine) error {
	base := client.MergeFrom(capiMachine.DeepCopy())
	delete(capiMachine.Annotations, preDrainHookAnnotation)
	if err := r.Patch(ctx, capiMachine, base); err != nil {
		return fmt.Errorf("error removing pre-drain hook: %w", err)
	}
	ctrl.LoggerFrom(ctx).V(1).Info("Removed pre-drain hook")
	return nil
}

func (r *MachinePoolLifecycleReconciler) evictMachinePool(ctx context.Context, poolName string) (bool, error) {
	machinePool := &computev1alpha1.MachinePool{}
	if err := r.IronCoreClient.Get(ctx, client.ObjectKey{Name: poolName}, machinePool); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("error getting machine pool: %w", err)
	}

	if err := r.ensureMaintenanceTaints(ctx, machinePool); err != nil {
		return false, err
	}

	hasMachines, err := r.hasNonToleratingMachines(ctx, poolName)
	if err != nil {
		return false, err
	}
	return !hasMachines, nil
}

func (r *MachinePoolLifecycleReconciler) ensureMaintenanceTaints(ctx context.Context, machinePool *computev1alpha1.MachinePool) error {
	desired := []commonv1alpha1.Taint{
		{Key: maintenanceTaintKey, Effect: commonv1alpha1.TaintEffectNoSchedule},
		{Key: maintenanceTaintKey, Effect: commonv1alpha1.TaintEffectNoExecute},
	}

	base := client.MergeFrom(machinePool.DeepCopy())

	added := false
	for _, taint := range desired {
		if !hasTaint(machinePool.Spec.Taints, taint) {
			machinePool.Spec.Taints = append(machinePool.Spec.Taints, taint)
			added = true
		}
	}
	if !added {
		return nil
	}

	if err := r.IronCoreClient.Patch(ctx, machinePool, base); err != nil {
		return fmt.Errorf("error applying maintenance taints: %w", err)
	}
	ctrl.LoggerFrom(ctx).V(1).Info("Applied maintenance taints")
	return nil
}

func (r *MachinePoolLifecycleReconciler) deleteMachinePool(ctx context.Context, poolName string) error {
	machinePool := &computev1alpha1.MachinePool{ObjectMeta: metav1.ObjectMeta{Name: poolName}}
	if err := r.IronCoreClient.Delete(ctx, machinePool); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("error deleting machine pool: %w", err)
	}
	ctrl.LoggerFrom(ctx).V(1).Info("Deleted MachinePool")
	return nil
}

func (r *MachinePoolLifecycleReconciler) hasNonToleratingMachines(ctx context.Context, poolName string) (bool, error) {
	machineList := &computev1alpha1.MachineList{}
	if err := r.IronCoreClient.List(ctx, machineList,
		client.MatchingFields{computev1alpha1.MachineMachinePoolRefNameField: poolName},
	); err != nil {
		return false, fmt.Errorf("error listing machines bound to pool: %w", err)
	}

	noExecuteTaint := commonv1alpha1.Taint{Key: maintenanceTaintKey, Effect: commonv1alpha1.TaintEffectNoExecute}
	for i := range machineList.Items {
		if !commonv1alpha1.ToleratesTaint(machineList.Items[i].Spec.Tolerations, &noExecuteTaint) {
			return true, nil
		}
	}
	return false, nil
}

func hasTaint(taints []commonv1alpha1.Taint, taint commonv1alpha1.Taint) bool {
	for _, t := range taints {
		if t.Key == taint.Key && t.Effect == taint.Effect {
			return true
		}
	}
	return false
}

func (r *MachinePoolLifecycleReconciler) SetupWithManager(mgr ctrl.Manager, ironCoreCache cache.Cache) error {
	return ctrl.NewControllerManagedBy(mgr).
		Named("compute-maintenance").
		For(
			&clusterv1.Machine{},
			builder.WithPredicates(r.selectorPredicate()),
		).
		WatchesRawSource(source.Kind(
			ironCoreCache,
			&computev1alpha1.Machine{},
			handler.TypedEnqueueRequestsFromMapFunc(r.enqueueCAPIMachinesForIronCoreMachine),
		)).
		Complete(r)
}

func (r *MachinePoolLifecycleReconciler) selectorPredicate() predicate.Predicate {
	return predicate.NewPredicateFuncs(func(obj client.Object) bool {
		capiMachine, ok := obj.(*clusterv1.Machine)
		if !ok {
			return false
		}
		return r.matchesSelector(capiMachine)
	})
}

func (r *MachinePoolLifecycleReconciler) enqueueCAPIMachinesForIronCoreMachine(ctx context.Context, ironCoreMachine *computev1alpha1.Machine) []reconcile.Request {
	if ironCoreMachine.Spec.MachinePoolRef == nil {
		return nil
	}
	poolName := ironCoreMachine.Spec.MachinePoolRef.Name

	capiMachineList := &clusterv1.MachineList{}
	if err := r.List(ctx, capiMachineList, client.MatchingFields{index.CAPIMachineNodeRefNameField: poolName}); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "Failed to list CAPI Machines for MachinePool", "machinePool", poolName)
		return nil
	}

	reqs := make([]reconcile.Request, 0, len(capiMachineList.Items))
	for i := range capiMachineList.Items {
		reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(&capiMachineList.Items[i])})
	}
	return reqs
}
