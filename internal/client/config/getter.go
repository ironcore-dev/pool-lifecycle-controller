// SPDX-FileCopyrightText: 2023 SAP SE or an SAP affiliate company and IronCore contributors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"crypto/x509"
	"crypto/x509/pkix"

	utilcertificate "github.com/ironcore-dev/ironcore/utils/certificate"
	"github.com/ironcore-dev/ironcore/utils/client/config"
	certificatesv1 "k8s.io/api/certificates/v1"
	"k8s.io/apiserver/pkg/server/egressselector"
)

const (
	Name  = "compute.ironcore.dev:system:pool-lifecycle-controller"
	Group = "compute.ironcore.dev:system:pool-lifecycle-controllers"
)

var (
	Getter = config.NewGetterOrDie(config.GetterOptions{
		Name:       Name,
		SignerName: certificatesv1.KubeAPIServerClientSignerName,
		Template: &x509.CertificateRequest{
			Subject: pkix.Name{
				CommonName:   Name,
				Organization: []string{Group},
			},
		},
		GetUsages:      utilcertificate.DefaultKubeAPIServerClientGetUsages,
		NetworkContext: egressselector.ControlPlane.AsNetworkContext(),
	})

	GetConfig = Getter.GetConfig

	GetConfigOrDie = Getter.GetConfigOrDie
)
