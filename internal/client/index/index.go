// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package index

import (
	"context"

	computev1alpha1 "github.com/ironcore-dev/ironcore/api/compute/v1alpha1"
	clusterv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const CAPIMachineNodeRefNameField = "status.nodeRef.name"

func SetupMachinePoolRefNameFieldIndexer(ctx context.Context, indexer client.FieldIndexer) error {
	return indexer.IndexField(ctx, &computev1alpha1.Machine{}, computev1alpha1.MachineMachinePoolRefNameField, func(obj client.Object) []string {
		machine, ok := obj.(*computev1alpha1.Machine)
		if !ok {
			return nil
		}
		if machine.Spec.MachinePoolRef == nil {
			return nil
		}
		return []string{machine.Spec.MachinePoolRef.Name}
	})
}

func SetupCAPIMachineNodeRefNameFieldIndexer(ctx context.Context, indexer client.FieldIndexer) error {
	return indexer.IndexField(ctx, &clusterv1.Machine{}, CAPIMachineNodeRefNameField, func(obj client.Object) []string {
		machine, ok := obj.(*clusterv1.Machine)
		if !ok {
			return nil
		}
		if !machine.Status.NodeRef.IsDefined() {
			return nil
		}
		return []string{machine.Status.NodeRef.Name}
	})
}
