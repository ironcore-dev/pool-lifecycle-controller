// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package controllers_test

import (
	commonv1alpha1 "github.com/ironcore-dev/ironcore/api/common/v1alpha1"
	computev1alpha1 "github.com/ironcore-dev/ironcore/api/compute/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "sigs.k8s.io/controller-runtime/pkg/envtest/komega"
)

const (
	maintenanceTaintKey    = "maintenance.ironcore.dev"
	preDrainHookAnnotation = clusterv1.PreDrainDeleteHookAnnotationPrefix + "/ironcore-maintenance"
	preDrainHookValue      = "ironcore-maintenance"
	machinePoolFinalizer   = "maintenance.ironcore.dev/machinepool-cleanup"
)

var (
	noScheduleTaint = commonv1alpha1.Taint{Key: maintenanceTaintKey, Effect: commonv1alpha1.TaintEffectNoSchedule}
	noExecuteTaint  = commonv1alpha1.Taint{Key: maintenanceTaintKey, Effect: commonv1alpha1.TaintEffectNoExecute}

	tolerateMaintenance = commonv1alpha1.Toleration{Key: maintenanceTaintKey, Operator: commonv1alpha1.TolerationOpExists}
)

var _ = Describe("MachinePoolLifecycleReconciler", func() {
	ns, machineClass := SetupTest()

	It("adds the cleanup finalizer and the pre-drain hook to a CAPI Machine", func(ctx SpecContext) {
		By("creating a CAPI Machine")
		capiMachine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-capi-machine-",
				Namespace:    ns.Name,
			},
			Spec: clusterv1.MachineSpec{
				ClusterName: "test-cluster",
				Bootstrap:   clusterv1.Bootstrap{DataSecretName: ptr.To("test-bootstrap")},
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: "infrastructure.cluster.x-k8s.io",
					Kind:     "GenericInfrastructureMachine",
					Name:     "test-infra",
				},
			},
		}
		Expect(k8sClient.Create(ctx, capiMachine)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, capiMachine))
		})

		By("asserting the reconciler adds the finalizer and the pre-drain hook")
		Eventually(Object(capiMachine)).Should(SatisfyAll(
			HaveField("ObjectMeta.Finalizers", ContainElement(machinePoolFinalizer)),
			HaveField("ObjectMeta.Annotations", HaveKeyWithValue(preDrainHookAnnotation, preDrainHookValue)),
		))
	})

	It("removes the hook and finalizer on deletion when the Machine has no node reference", func(ctx SpecContext) {
		By("creating a CAPI Machine without a node reference")
		capiMachine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-capi-machine-",
				Namespace:    ns.Name,
			},
			Spec: clusterv1.MachineSpec{
				ClusterName: "test-cluster",
				Bootstrap:   clusterv1.Bootstrap{DataSecretName: ptr.To("test-bootstrap")},
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: "infrastructure.cluster.x-k8s.io",
					Kind:     "GenericInfrastructureMachine",
					Name:     "test-infra",
				},
			},
		}
		Expect(k8sClient.Create(ctx, capiMachine)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, capiMachine))
		})

		By("waiting for the reconciler to take ownership of the Machine")
		Eventually(Object(capiMachine)).Should(
			HaveField("ObjectMeta.Finalizers", ContainElement(machinePoolFinalizer)),
		)

		By("deleting the Machine that never got a node reference")
		Expect(k8sClient.Delete(ctx, capiMachine)).To(Succeed())

		By("observing the Machine is fully released")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(capiMachine), &clusterv1.Machine{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the Machine to be gone")
		}).Should(Succeed())
	})

	It("taints the pool and holds the hook while non-tolerating IronCore Machines remain", func(ctx SpecContext) {
		By("creating a MachinePool")
		pool := &computev1alpha1.MachinePool{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-mp-"},
			Spec:       computev1alpha1.MachinePoolSpec{ProviderID: "test://pool"},
		}
		Expect(k8sClient.Create(ctx, pool)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, pool))
		})

		By("creating an IronCore Machine that does not tolerate the NoExecute maintenance taint")
		ironCoreMachine := &computev1alpha1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-machine-",
				Namespace:    ns.Name,
			},
			Spec: computev1alpha1.MachineSpec{
				MachineClassRef: corev1.LocalObjectReference{Name: machineClass.Name},
				MachinePoolRef:  &corev1.LocalObjectReference{Name: pool.Name},
			},
		}
		Expect(k8sClient.Create(ctx, ironCoreMachine)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, ironCoreMachine))
		})

		By("creating a CAPI Machine referencing the pool and waiting for the reconciler to own it")
		capiMachine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-capi-machine-",
				Namespace:    ns.Name,
			},
			Spec: clusterv1.MachineSpec{
				ClusterName: "test-cluster",
				Bootstrap:   clusterv1.Bootstrap{DataSecretName: ptr.To("test-bootstrap")},
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: "infrastructure.cluster.x-k8s.io",
					Kind:     "GenericInfrastructureMachine",
					Name:     "test-infra",
				},
			},
		}
		Expect(k8sClient.Create(ctx, capiMachine)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, capiMachine))
		})

		Eventually(UpdateStatus(capiMachine, func() {
			capiMachine.Status.NodeRef = clusterv1.MachineNodeReference{Name: pool.Name}
		})).Should(Succeed())

		Eventually(Object(capiMachine)).Should(
			HaveField("ObjectMeta.Finalizers", ContainElement(machinePoolFinalizer)),
		)

		By("deleting the CAPI Machine")
		Expect(k8sClient.Delete(ctx, capiMachine)).To(Succeed())

		By("applying both maintenance taints to the pool")
		Eventually(Object(pool)).Should(
			HaveField("Spec.Taints", ConsistOf(noScheduleTaint, noExecuteTaint)),
		)

		By("keeping the hook, finalizer and pool in place while the pool is not evacuated")
		Consistently(Object(capiMachine)).Should(SatisfyAll(
			HaveField("ObjectMeta.Finalizers", ContainElement(machinePoolFinalizer)),
			HaveField("ObjectMeta.Annotations", HaveKeyWithValue(preDrainHookAnnotation, preDrainHookValue)),
		))
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())
	})

	It("releases the hook, deletes the pool and finalizer once the pool is evacuated", func(ctx SpecContext) {
		By("creating a MachinePool")
		pool := &computev1alpha1.MachinePool{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-mp-"},
			Spec:       computev1alpha1.MachinePoolSpec{ProviderID: "test://pool"},
		}
		Expect(k8sClient.Create(ctx, pool)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, pool))
		})

		By("creating an IronCore Machine that tolerates the maintenance taint")
		ironCoreMachine := &computev1alpha1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-machine-",
				Namespace:    ns.Name,
			},
			Spec: computev1alpha1.MachineSpec{
				MachineClassRef: corev1.LocalObjectReference{Name: machineClass.Name},
				MachinePoolRef:  &corev1.LocalObjectReference{Name: pool.Name},
				Tolerations:     []commonv1alpha1.Toleration{tolerateMaintenance},
			},
		}
		Expect(k8sClient.Create(ctx, ironCoreMachine)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, ironCoreMachine))
		})

		By("creating a CAPI Machine referencing the pool and waiting for the reconciler to own it")
		capiMachine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-capi-machine-",
				Namespace:    ns.Name,
			},
			Spec: clusterv1.MachineSpec{
				ClusterName: "test-cluster",
				Bootstrap:   clusterv1.Bootstrap{DataSecretName: ptr.To("test-bootstrap")},
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: "infrastructure.cluster.x-k8s.io",
					Kind:     "GenericInfrastructureMachine",
					Name:     "test-infra",
				},
			},
		}
		Expect(k8sClient.Create(ctx, capiMachine)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, capiMachine))
		})
		Eventually(UpdateStatus(capiMachine, func() {
			capiMachine.Status.NodeRef = clusterv1.MachineNodeReference{Name: pool.Name}
		})).Should(Succeed())
		Eventually(Object(capiMachine)).Should(
			HaveField("ObjectMeta.Finalizers", ContainElement(machinePoolFinalizer)),
		)

		By("deleting the CAPI Machine")
		Expect(k8sClient.Delete(ctx, capiMachine)).To(Succeed())

		By("deleting the pool and releasing the Machine")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pool), &computev1alpha1.MachinePool{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the MachinePool to be deleted")
		}).Should(Succeed())
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(capiMachine), &clusterv1.Machine{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the Machine to be gone")
		}).Should(Succeed())
	})

	It("waits for CAPI to drain the node before deleting the pool", func(ctx SpecContext) {
		By("creating a MachinePool")
		pool := &computev1alpha1.MachinePool{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-mp-"},
			Spec:       computev1alpha1.MachinePoolSpec{ProviderID: "test://pool"},
		}
		Expect(k8sClient.Create(ctx, pool)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, pool))
		})

		By("creating an IronCore Machine that tolerates the maintenance taint")
		ironCoreMachine := &computev1alpha1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-machine-",
				Namespace:    ns.Name,
			},
			Spec: computev1alpha1.MachineSpec{
				MachineClassRef: corev1.LocalObjectReference{Name: machineClass.Name},
				MachinePoolRef:  &corev1.LocalObjectReference{Name: pool.Name},
				Tolerations:     []commonv1alpha1.Toleration{tolerateMaintenance},
			},
		}
		Expect(k8sClient.Create(ctx, ironCoreMachine)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, ironCoreMachine))
		})

		By("creating a CAPI Machine referencing the pool and waiting for the reconciler to own it")
		capiMachine := &clusterv1.Machine{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-capi-machine-",
				Namespace:    ns.Name,
			},
			Spec: clusterv1.MachineSpec{
				ClusterName: "test-cluster",
				Bootstrap:   clusterv1.Bootstrap{DataSecretName: ptr.To("test-bootstrap")},
				InfrastructureRef: clusterv1.ContractVersionedObjectReference{
					APIGroup: "infrastructure.cluster.x-k8s.io",
					Kind:     "GenericInfrastructureMachine",
					Name:     "test-infra",
				},
			},
		}
		Expect(k8sClient.Create(ctx, capiMachine)).To(Succeed())
		DeferCleanup(func(ctx SpecContext) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, capiMachine))
		})
		Eventually(UpdateStatus(capiMachine, func() {
			capiMachine.Status.NodeRef = clusterv1.MachineNodeReference{Name: pool.Name}
		})).Should(Succeed())
		Eventually(Object(capiMachine)).Should(
			HaveField("ObjectMeta.Finalizers", ContainElement(machinePoolFinalizer)))

		By("also holding the CAPI Machine finalizer, standing in for the CAPI machine controller")
		Eventually(Update(capiMachine, func() {
			controllerutil.AddFinalizer(capiMachine, clusterv1.MachineFinalizer)
		})).Should(Succeed())

		By("deleting the CAPI Machine")
		Expect(k8sClient.Delete(ctx, capiMachine)).To(Succeed())

		By("releasing the hook but leaving the pool and our finalizer while CAPI still owns the Machine")
		Eventually(Object(capiMachine)).Should(
			HaveField("ObjectMeta.Annotations", Not(HaveKey(preDrainHookAnnotation))),
		)
		Consistently(Object(capiMachine)).Should(
			HaveField("ObjectMeta.Finalizers", ContainElement(machinePoolFinalizer)),
		)
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pool), pool)).To(Succeed())

		By("removing the CAPI finalizer so CAPI has finished draining the node")
		Eventually(Update(capiMachine, func() {
			controllerutil.RemoveFinalizer(capiMachine, clusterv1.MachineFinalizer)
		})).Should(Succeed())

		By("deleting the pool and releasing the Machine")
		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(pool), &computev1alpha1.MachinePool{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the MachinePool to be deleted")
		}).Should(Succeed())

		Eventually(func(g Gomega) {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(capiMachine), &clusterv1.Machine{})
			g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "expected the Machine to be gone")
		}).Should(Succeed())
	})
})
