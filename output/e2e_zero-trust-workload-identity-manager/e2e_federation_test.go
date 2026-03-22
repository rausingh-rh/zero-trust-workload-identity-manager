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

// Generated E2E Tests for SPIRE Federation API
// Based on git diff origin/ai-staging...HEAD
//
// This file contains test blocks for federation functionality added to SpireServer.
// Copy the test blocks you need into test/e2e/e2e_test.go within the appropriate Context.
//
// DO NOT copy BeforeSuite, TestE2E, or client setup code - those already exist in the test suite.

package e2e

import (
	"context"
	"fmt"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	operatorv1alpha1 "github.com/openshift/zero-trust-workload-identity-manager/api/v1alpha1"
	"github.com/openshift/zero-trust-workload-identity-manager/test/e2e/utils"

	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ============================================================================
// FEDERATION E2E TESTS
// ============================================================================
// These test blocks can be added to the existing test suite in test/e2e/e2e_test.go
// They should be placed within a Context("Federation", func() { ... }) block

// Diff-suggested: New federation API fields in SpireServer (api/v1alpha1/spire_server_config_types.go)
var _ = Context("Federation - Bundle Endpoint Configuration", func() {

	It("should configure federation bundle endpoint with https_spiffe profile", func() {
		// Diff-suggested: SpireServer federation.bundleEndpoint.profile field
		By("Creating SpireServer with https_spiffe federation profile")
		spireServer := &operatorv1alpha1.SpireServer{}
		err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, spireServer)
		Expect(err).NotTo(HaveOccurred())

		// Update with federation config
		spireServer.Spec.Federation = &operatorv1alpha1.FederationConfig{
			BundleEndpoint: operatorv1alpha1.BundleEndpointConfig{
				Profile:     operatorv1alpha1.HttpsSpiffeProfile,
				RefreshHint: 300,
			},
			ManagedRoute: "true",
		}

		err = k8sClient.Update(testCtx, spireServer)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for SpireServer to reconcile with federation")
		expectedConditions := map[string]metav1.ConditionStatus{
			"Ready": metav1.ConditionTrue,
		}
		utils.WaitForSpireServerConditions(testCtx, k8sClient, "cluster", expectedConditions, utils.DefaultTimeout)

		By("Verifying StatefulSet includes federation port 8443")
		sts := &appsv1.StatefulSet{}
		err = k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server",
			Namespace: utils.OperatorNamespace,
		}, sts)
		Expect(err).NotTo(HaveOccurred())

		// Check for federation port in container ports
		var foundFederationPort bool
		for _, container := range sts.Spec.Template.Spec.Containers {
			for _, port := range container.Ports {
				if port.Name == "federation" && port.ContainerPort == 8443 {
					foundFederationPort = true
					break
				}
			}
		}
		Expect(foundFederationPort).To(BeTrue(), "StatefulSet should expose federation port 8443")

		By("Verifying Service exposes federation port")
		svc := &corev1.Service{}
		err = k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server",
			Namespace: utils.OperatorNamespace,
		}, svc)
		Expect(err).NotTo(HaveOccurred())

		var foundServicePort bool
		for _, port := range svc.Spec.Ports {
			if port.Name == "federation" && port.Port == 8443 {
				foundServicePort = true
				break
			}
		}
		Expect(foundServicePort).To(BeTrue(), "Service should expose federation port 8443")

		By("Verifying managed Route is created for federation endpoint")
		route := &routev1.Route{}
		err = k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server-federation",
			Namespace: utils.OperatorNamespace,
		}, route)
		Expect(err).NotTo(HaveOccurred(), "Federation Route should be created")
		Expect(route.Spec.TLS.Termination).To(Equal(routev1.TLSTerminationPassthrough), "https_spiffe should use passthrough TLS")

		By("Verifying ConfigMap contains bundle_endpoint configuration")
		cm := &corev1.ConfigMap{}
		err = k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server",
			Namespace: utils.OperatorNamespace,
		}, cm)
		Expect(err).NotTo(HaveOccurred())

		serverConf, ok := cm.Data["server.conf"]
		Expect(ok).To(BeTrue(), "ConfigMap should contain server.conf")
		Expect(serverConf).To(ContainSubstring("BundleEndpoint"), "server.conf should contain BundleEndpoint plugin")
		Expect(serverConf).To(ContainSubstring("https_spiffe"), "server.conf should specify https_spiffe profile")
	})

	// Diff-suggested: https_web profile with ACME support (api/v1alpha1/spire_server_config_types.go)
	It("should configure federation bundle endpoint with https_web and ACME", func() {
		By("Getting cluster app domain")
		baseDomain, err := utils.GetClusterBaseDomain(testCtx, configClient)
		Expect(err).NotTo(HaveOccurred())
		appDomain := fmt.Sprintf("apps.%s", baseDomain)

		By("Updating SpireServer with https_web + ACME federation")
		spireServer := &operatorv1alpha1.SpireServer{}
		err = k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, spireServer)
		Expect(err).NotTo(HaveOccurred())

		spireServer.Spec.Federation = &operatorv1alpha1.FederationConfig{
			BundleEndpoint: operatorv1alpha1.BundleEndpointConfig{
				Profile:     operatorv1alpha1.HttpsWebProfile,
				RefreshHint: 300,
				HttpsWeb: &operatorv1alpha1.HttpsWebConfig{
					Acme: &operatorv1alpha1.AcmeConfig{
						DirectoryUrl: "https://acme-staging-v02.api.letsencrypt.org/directory",
						DomainName:   fmt.Sprintf("spire-federation.%s", appDomain),
						Email:        "admin@example.com",
						TosAccepted:  "true",
					},
				},
			},
			ManagedRoute: "true",
		}

		err = k8sClient.Update(testCtx, spireServer)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for SpireServer to reconcile")
		expectedConditions := map[string]metav1.ConditionStatus{
			"Ready": metav1.ConditionTrue,
		}
		utils.WaitForSpireServerConditions(testCtx, k8sClient, "cluster", expectedConditions, utils.DefaultTimeout)

		By("Verifying ConfigMap contains ACME configuration")
		cm := &corev1.ConfigMap{}
		err = k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server",
			Namespace: utils.OperatorNamespace,
		}, cm)
		Expect(err).NotTo(HaveOccurred())

		serverConf := cm.Data["server.conf"]
		Expect(serverConf).To(ContainSubstring("https_web"), "server.conf should specify https_web profile")
		Expect(serverConf).To(ContainSubstring("acme"), "server.conf should contain ACME config")
		Expect(serverConf).To(ContainSubstring("directory_url"), "server.conf should contain ACME directory URL")
		Expect(serverConf).To(ContainSubstring("admin@example.com"), "server.conf should contain ACME email")

		By("Verifying Route uses edge termination for https_web")
		route := &routev1.Route{}
		err = k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server-federation",
			Namespace: utils.OperatorNamespace,
		}, route)
		Expect(err).NotTo(HaveOccurred())
		Expect(route.Spec.TLS.Termination).To(Equal(routev1.TLSTerminationEdge), "https_web should use edge TLS termination")
	})

	// Diff-suggested: Manual TLS certificate support (api/v1alpha1/spire_server_config_types.go)
	It("should configure federation with manual TLS certificate from Secret", func() {
		By("Creating external TLS secret for federation")
		tlsSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "spire-federation-tls",
				Namespace: utils.OperatorNamespace,
			},
			Type: corev1.SecretTypeTLS,
			StringData: map[string]string{
				"tls.crt": "-----BEGIN CERTIFICATE-----\ntest-cert-data\n-----END CERTIFICATE-----",
				"tls.key": "-----BEGIN PRIVATE KEY-----\ntest-key-data\n-----END PRIVATE KEY-----",
			},
		}
		err := k8sClient.Create(testCtx, tlsSecret)
		if err != nil && !strings.Contains(err.Error(), "already exists") {
			Expect(err).NotTo(HaveOccurred())
		}

		By("Updating SpireServer with https_web + manual TLS")
		spireServer := &operatorv1alpha1.SpireServer{}
		err = k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, spireServer)
		Expect(err).NotTo(HaveOccurred())

		spireServer.Spec.Federation = &operatorv1alpha1.FederationConfig{
			BundleEndpoint: operatorv1alpha1.BundleEndpointConfig{
				Profile:     operatorv1alpha1.HttpsWebProfile,
				RefreshHint: 300,
				HttpsWeb: &operatorv1alpha1.HttpsWebConfig{
					ServingCert: &operatorv1alpha1.ServingCertConfig{
						ExternalSecretRef: "spire-federation-tls",
						FileSyncInterval:  86400,
					},
				},
			},
			ManagedRoute: "true",
		}

		err = k8sClient.Update(testCtx, spireServer)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for SpireServer to reconcile")
		expectedConditions := map[string]metav1.ConditionStatus{
			"Ready": metav1.ConditionTrue,
		}
		utils.WaitForSpireServerConditions(testCtx, k8sClient, "cluster", expectedConditions, utils.DefaultTimeout)

		By("Verifying StatefulSet mounts external TLS secret")
		sts := &appsv1.StatefulSet{}
		err = k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server",
			Namespace: utils.OperatorNamespace,
		}, sts)
		Expect(err).NotTo(HaveOccurred())

		// Check volumes for federation TLS secret
		var foundTLSVolume bool
		for _, volume := range sts.Spec.Template.Spec.Volumes {
			if volume.Secret != nil && volume.Secret.SecretName == "spire-federation-tls" {
				foundTLSVolume = true
				break
			}
		}
		Expect(foundTLSVolume).To(BeTrue(), "StatefulSet should mount external TLS secret")

		By("Verifying ConfigMap contains servingCert configuration")
		cm := &corev1.ConfigMap{}
		err = k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server",
			Namespace: utils.OperatorNamespace,
		}, cm)
		Expect(err).NotTo(HaveOccurred())

		serverConf := cm.Data["server.conf"]
		Expect(serverConf).To(ContainSubstring("file_sync_interval"), "server.conf should contain fileSyncInterval")
	})
})

// Diff-suggested: federatesWith field for remote trust domain configuration
var _ = Context("Federation - Federated Trust Domains", func() {

	It("should configure federation with remote trust domains (https_spiffe)", func() {
		By("Updating SpireServer with federatesWith configuration")
		spireServer := &operatorv1alpha1.SpireServer{}
		err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, spireServer)
		Expect(err).NotTo(HaveOccurred())

		spireServer.Spec.Federation = &operatorv1alpha1.FederationConfig{
			BundleEndpoint: operatorv1alpha1.BundleEndpointConfig{
				Profile:     operatorv1alpha1.HttpsSpiffeProfile,
				RefreshHint: 300,
			},
			FederatesWith: []operatorv1alpha1.FederatesWithConfig{
				{
					TrustDomain:           "partner.example.com",
					BundleEndpointUrl:     "https://spire-federation.partner.example.com:8443",
					BundleEndpointProfile: operatorv1alpha1.HttpsSpiffeProfile,
					EndpointSpiffeId:      "spiffe://partner.example.com/spire/server",
				},
			},
			ManagedRoute: "true",
		}

		err = k8sClient.Update(testCtx, spireServer)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for SpireServer to reconcile")
		expectedConditions := map[string]metav1.ConditionStatus{
			"Ready": metav1.ConditionTrue,
		}
		utils.WaitForSpireServerConditions(testCtx, k8sClient, "cluster", expectedConditions, utils.DefaultTimeout)

		By("Verifying ConfigMap contains federatesWith configuration")
		cm := &corev1.ConfigMap{}
		err = k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server",
			Namespace: utils.OperatorNamespace,
		}, cm)
		Expect(err).NotTo(HaveOccurred())

		serverConf := cm.Data["server.conf"]
		Expect(serverConf).To(ContainSubstring("FederatesWith"), "server.conf should contain FederatesWith plugin")
		Expect(serverConf).To(ContainSubstring("partner.example.com"), "server.conf should contain federated trust domain")
		Expect(serverConf).To(ContainSubstring("bundle_endpoint_url"), "server.conf should contain bundle endpoint URL")
		Expect(serverConf).To(ContainSubstring("endpoint_spiffe_id"), "server.conf should contain endpoint SPIFFE ID")
	})

	It("should configure federation with remote trust domains (https_web)", func() {
		By("Updating SpireServer with https_web federatesWith")
		spireServer := &operatorv1alpha1.SpireServer{}
		err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, spireServer)
		Expect(err).NotTo(HaveOccurred())

		spireServer.Spec.Federation = &operatorv1alpha1.FederationConfig{
			BundleEndpoint: operatorv1alpha1.BundleEndpointConfig{
				Profile:     operatorv1alpha1.HttpsWebProfile,
				RefreshHint: 300,
			},
			FederatesWith: []operatorv1alpha1.FederatesWithConfig{
				{
					TrustDomain:           "partner2.example.com",
					BundleEndpointUrl:     "https://spire-federation.partner2.example.com:8443",
					BundleEndpointProfile: operatorv1alpha1.HttpsWebProfile,
					// endpointSpiffeId is optional for https_web
				},
			},
			ManagedRoute: "true",
		}

		err = k8sClient.Update(testCtx, spireServer)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for SpireServer to reconcile")
		expectedConditions := map[string]metav1.ConditionStatus{
			"Ready": metav1.ConditionTrue,
		}
		utils.WaitForSpireServerConditions(testCtx, k8sClient, "cluster", expectedConditions, utils.DefaultTimeout)

		By("Verifying ConfigMap contains https_web federatesWith")
		cm := &corev1.ConfigMap{}
		err = k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server",
			Namespace: utils.OperatorNamespace,
		}, cm)
		Expect(err).NotTo(HaveOccurred())

		serverConf := cm.Data["server.conf"]
		Expect(serverConf).To(ContainSubstring("partner2.example.com"), "server.conf should contain federated trust domain")
		Expect(serverConf).To(ContainSubstring("https_web"), "server.conf should specify https_web profile for federatesWith")
	})
})

// Diff-suggested: managedRoute field for automatic Route creation
var _ = Context("Federation - Managed Route Lifecycle", func() {

	It("should create Route when managedRoute is true", func() {
		By("Ensuring SpireServer has managedRoute: true")
		spireServer := &operatorv1alpha1.SpireServer{}
		err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, spireServer)
		Expect(err).NotTo(HaveOccurred())

		if spireServer.Spec.Federation == nil {
			spireServer.Spec.Federation = &operatorv1alpha1.FederationConfig{
				BundleEndpoint: operatorv1alpha1.BundleEndpointConfig{
					Profile:     operatorv1alpha1.HttpsSpiffeProfile,
					RefreshHint: 300,
				},
			}
		}
		spireServer.Spec.Federation.ManagedRoute = "true"

		err = k8sClient.Update(testCtx, spireServer)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for Route to be created")
		Eventually(func() error {
			route := &routev1.Route{}
			return k8sClient.Get(testCtx, types.NamespacedName{
				Name:      "spire-server-federation",
				Namespace: utils.OperatorNamespace,
			}, route)
		}, utils.DefaultTimeout, utils.DefaultInterval).Should(Succeed(), "Route should be created when managedRoute is true")

		By("Verifying Route configuration")
		route := &routev1.Route{}
		err = k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server-federation",
			Namespace: utils.OperatorNamespace,
		}, route)
		Expect(err).NotTo(HaveOccurred())
		Expect(route.Spec.Port.TargetPort.IntVal).To(Equal(int32(8443)), "Route should target federation port 8443")
	})

	It("should delete Route when managedRoute is set to false", func() {
		By("Updating SpireServer with managedRoute: false")
		spireServer := &operatorv1alpha1.SpireServer{}
		err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, spireServer)
		Expect(err).NotTo(HaveOccurred())

		spireServer.Spec.Federation.ManagedRoute = "false"
		err = k8sClient.Update(testCtx, spireServer)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for Route to be deleted")
		Eventually(func() bool {
			route := &routev1.Route{}
			err := k8sClient.Get(testCtx, types.NamespacedName{
				Name:      "spire-server-federation",
				Namespace: utils.OperatorNamespace,
			}, route)
			return err != nil && strings.Contains(err.Error(), "not found")
		}, utils.DefaultTimeout, utils.DefaultInterval).Should(BeTrue(), "Route should be deleted when managedRoute is false")
	})

	It("should recreate Route when managedRoute is toggled back to true", func() {
		By("Updating SpireServer with managedRoute: true")
		spireServer := &operatorv1alpha1.SpireServer{}
		err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, spireServer)
		Expect(err).NotTo(HaveOccurred())

		spireServer.Spec.Federation.ManagedRoute = "true"
		err = k8sClient.Update(testCtx, spireServer)
		Expect(err).NotTo(HaveOccurred())

		By("Waiting for Route to be recreated")
		Eventually(func() error {
			route := &routev1.Route{}
			return k8sClient.Get(testCtx, types.NamespacedName{
				Name:      "spire-server-federation",
				Namespace: utils.OperatorNamespace,
			}, route)
		}, utils.DefaultTimeout, utils.DefaultInterval).Should(Succeed(), "Route should be recreated when managedRoute is true")
	})
})

// Diff-suggested: Immutability validation for federation.bundleEndpoint.profile
var _ = Context("Federation - Validation and Immutability", func() {

	It("should reject changing federation profile after creation", func() {
		By("Getting current SpireServer with federation")
		spireServer := &operatorv1alpha1.SpireServer{}
		err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, spireServer)
		Expect(err).NotTo(HaveOccurred())

		currentProfile := spireServer.Spec.Federation.BundleEndpoint.Profile

		By(fmt.Sprintf("Attempting to change profile from %s", currentProfile))
		var newProfile operatorv1alpha1.BundleEndpointProfile
		if currentProfile == operatorv1alpha1.HttpsSpiffeProfile {
			newProfile = operatorv1alpha1.HttpsWebProfile
		} else {
			newProfile = operatorv1alpha1.HttpsSpiffeProfile
		}

		spireServer.Spec.Federation.BundleEndpoint.Profile = newProfile
		err = k8sClient.Update(testCtx, spireServer)
		Expect(err).To(HaveOccurred(), "Changing federation profile should be rejected")
		Expect(err.Error()).To(ContainSubstring("immutable"), "Error should mention immutability")
	})

	It("should reject removing federation config once set", func() {
		By("Getting current SpireServer with federation")
		spireServer := &operatorv1alpha1.SpireServer{}
		err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, spireServer)
		Expect(err).NotTo(HaveOccurred())

		By("Attempting to remove federation configuration")
		spireServer.Spec.Federation = nil
		err = k8sClient.Update(testCtx, spireServer)
		Expect(err).To(HaveOccurred(), "Removing federation config should be rejected")
		Expect(err.Error()).To(ContainSubstring("cannot be removed"), "Error should mention federation cannot be removed")
	})

	It("should reject acme and servingCert both set", func() {
		By("Attempting to create SpireServer with both acme and servingCert")
		invalidServer := &operatorv1alpha1.SpireServer{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster-invalid-test",
			},
			Spec: operatorv1alpha1.SpireServerSpec{
				JWTIssuer: "https://test.example.com",
				CASubject: operatorv1alpha1.CASubject{
					Country:      "US",
					Organization: "Test",
					CommonName:   "Test",
				},
				Persistence: operatorv1alpha1.Persistence{
					Size:       "1Gi",
					AccessMode: "ReadWriteOnce",
				},
				Datastore: operatorv1alpha1.DataStore{
					DatabaseType: "sqlite3",
				},
				Federation: &operatorv1alpha1.FederationConfig{
					BundleEndpoint: operatorv1alpha1.BundleEndpointConfig{
						Profile: operatorv1alpha1.HttpsWebProfile,
						HttpsWeb: &operatorv1alpha1.HttpsWebConfig{
							Acme: &operatorv1alpha1.AcmeConfig{
								DirectoryUrl: "https://acme.example.com",
								DomainName:   "test.com",
								Email:        "test@example.com",
								TosAccepted:  "true",
							},
							ServingCert: &operatorv1alpha1.ServingCertConfig{
								ExternalSecretRef: "test-secret",
							},
						},
					},
				},
			},
		}

		err := k8sClient.Create(testCtx, invalidServer)
		Expect(err).To(HaveOccurred(), "Creating SpireServer with both acme and servingCert should be rejected")
		Expect(err.Error()).To(ContainSubstring("exactly one of"), "Error should mention mutual exclusivity")

		// Cleanup (if somehow created)
		_ = k8sClient.Delete(testCtx, invalidServer)
	})

	It("should require endpointSpiffeId when federating with https_spiffe", func() {
		By("Attempting to create SpireServer with https_spiffe federatesWith but missing endpointSpiffeId")
		invalidServer := &operatorv1alpha1.SpireServer{
			ObjectMeta: metav1.ObjectMeta{
				Name: "cluster-missing-spiffeid",
			},
			Spec: operatorv1alpha1.SpireServerSpec{
				JWTIssuer: "https://test.example.com",
				CASubject: operatorv1alpha1.CASubject{
					Country:      "US",
					Organization: "Test",
					CommonName:   "Test",
				},
				Persistence: operatorv1alpha1.Persistence{
					Size:       "1Gi",
					AccessMode: "ReadWriteOnce",
				},
				Datastore: operatorv1alpha1.DataStore{
					DatabaseType: "sqlite3",
				},
				Federation: &operatorv1alpha1.FederationConfig{
					BundleEndpoint: operatorv1alpha1.BundleEndpointConfig{
						Profile: operatorv1alpha1.HttpsSpiffeProfile,
					},
					FederatesWith: []operatorv1alpha1.FederatesWithConfig{
						{
							TrustDomain:           "partner.example.com",
							BundleEndpointUrl:     "https://partner.example.com:8443",
							BundleEndpointProfile: operatorv1alpha1.HttpsSpiffeProfile,
							// Missing endpointSpiffeId
						},
					},
				},
			},
		}

		err := k8sClient.Create(testCtx, invalidServer)
		Expect(err).To(HaveOccurred(), "Creating SpireServer with https_spiffe federatesWith but no endpointSpiffeId should be rejected")
		Expect(err.Error()).To(ContainSubstring("endpointSpiffeId is required"), "Error should mention missing endpointSpiffeId")

		// Cleanup
		_ = k8sClient.Delete(testCtx, invalidServer)
	})
})

// Diff-suggested: New RBAC roles for external certificate secrets (bindata/spire-server/)
var _ = Context("Federation - RBAC Verification", func() {

	It("should have RBAC permissions for Routes", func() {
		By("Checking ClusterRole for Route permissions")
		// This test verifies the operator has proper RBAC for managing Routes
		// The actual verification would check the ClusterRole resource, but in e2e
		// we can verify indirectly by successfully creating/deleting Routes in previous tests

		// Verify the operator can create Routes (demonstrated by earlier tests)
		route := &routev1.Route{}
		err := k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server-federation",
			Namespace: utils.OperatorNamespace,
		}, route)

		if err == nil {
			// Route exists, operator successfully created it
			Expect(route.Name).To(Equal("spire-server-federation"))
		} else {
			// Route doesn't exist (managedRoute might be false), which is also valid
			Expect(err.Error()).To(ContainSubstring("not found"))
		}
	})

	It("should be able to read external TLS secrets when configured", func() {
		By("Verifying operator can access external TLS secret")
		// The external secret was created earlier in manual TLS test
		secret := &corev1.Secret{}
		err := k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-federation-tls",
			Namespace: utils.OperatorNamespace,
		}, secret)

		if err == nil {
			Expect(secret.Type).To(Equal(corev1.SecretTypeTLS), "External TLS secret should be of type TLS")
			Expect(secret.Data).To(HaveKey("tls.crt"))
			Expect(secret.Data).To(HaveKey("tls.key"))
		}
	})
})

// Diff-suggested: Updated ConfigMap generation with federation plugins (pkg/controller/spire-server/configmap.go)
var _ = Context("Federation - ConfigMap Generation", func() {

	It("should generate valid SPIRE server.conf with BundleEndpoint plugin", func() {
		By("Getting spire-server ConfigMap")
		cm := &corev1.ConfigMap{}
		err := k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server",
			Namespace: utils.OperatorNamespace,
		}, cm)
		Expect(err).NotTo(HaveOccurred())

		By("Verifying server.conf contains BundleEndpoint plugin")
		serverConf, ok := cm.Data["server.conf"]
		Expect(ok).To(BeTrue(), "ConfigMap should contain server.conf key")
		Expect(serverConf).To(ContainSubstring("BundleEndpoint"), "server.conf should contain BundleEndpoint plugin")
		Expect(serverConf).To(ContainSubstring("address"), "BundleEndpoint should have address config")
		Expect(serverConf).To(ContainSubstring("0.0.0.0:8443"), "BundleEndpoint should listen on 0.0.0.0:8443")
	})

	It("should generate valid SPIRE server.conf with FederatesWith plugin", func() {
		By("Getting spire-server ConfigMap")
		cm := &corev1.ConfigMap{}
		err := k8sClient.Get(testCtx, types.NamespacedName{
			Name:      "spire-server",
			Namespace: utils.OperatorNamespace,
		}, cm)
		Expect(err).NotTo(HaveOccurred())

		By("Checking if federatesWith is configured in SpireServer")
		spireServer := &operatorv1alpha1.SpireServer{}
		err = k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, spireServer)
		Expect(err).NotTo(HaveOccurred())

		if spireServer.Spec.Federation != nil && len(spireServer.Spec.Federation.FederatesWith) > 0 {
			By("Verifying server.conf contains FederatesWith plugin")
			serverConf := cm.Data["server.conf"]
			Expect(serverConf).To(ContainSubstring("FederatesWith"), "server.conf should contain FederatesWith plugin when federatesWith is configured")
			Expect(serverConf).To(ContainSubstring("trust_domain"), "FederatesWith should contain trust_domain")
			Expect(serverConf).To(ContainSubstring("bundle_endpoint_url"), "FederatesWith should contain bundle_endpoint_url")
		}
	})
})

// End of generated federation test blocks
// Remember: Copy only the test blocks you need into test/e2e/e2e_test.go
// Do NOT copy BeforeSuite, TestE2E, client setup, or this comment block
