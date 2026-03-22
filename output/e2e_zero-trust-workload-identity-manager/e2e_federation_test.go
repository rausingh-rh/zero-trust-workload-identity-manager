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

package e2e

import (
	"context"
	"encoding/json"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	operatorv1alpha1 "github.com/openshift/zero-trust-workload-identity-manager/api/v1alpha1"
	"github.com/openshift/zero-trust-workload-identity-manager/test/e2e/utils"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	routev1 "github.com/openshift/api/route/v1"
)

// Diff-suggested: SPIRE federation support added in EP-1863
var _ = Describe("SPIRE Federation", Ordered, func() {
	var testCtx context.Context
	var appDomain string
	var jwtIssuer string

	BeforeAll(func() {
		By("Getting cluster base domain")
		baseDomain, err := utils.GetClusterBaseDomain(context.Background(), configClient)
		Expect(err).NotTo(HaveOccurred(), "failed to get cluster base domain")
		appDomain = fmt.Sprintf("apps.%s", baseDomain)
		jwtIssuer = fmt.Sprintf("https://oidc-discovery.%s", appDomain)
	})

	BeforeEach(func() {
		var cancel context.CancelFunc
		testCtx, cancel = context.WithTimeout(context.Background(), utils.DefaultTimeout)
		DeferCleanup(cancel)
	})

	// Diff-suggested: New federation field added to SpireServerSpec
	Context("Federation with https_spiffe profile", func() {
		AfterAll(func() {
			By("Cleaning up SpireServer with federation")
			server := &operatorv1alpha1.SpireServer{}
			err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "cluster"}, server)
			if err == nil {
				Expect(k8sClient.Delete(context.Background(), server)).To(Succeed())
			}
		})

		It("Should create SpireServer with federation using https_spiffe profile", func() {
			By("Creating SpireServer CR with federation enabled")
			server := &operatorv1alpha1.SpireServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: operatorv1alpha1.SpireServerSpec{
					JwtIssuer: jwtIssuer,
					CASubject: operatorv1alpha1.CASubject{
						Country: "US",
					},
					Persistence: operatorv1alpha1.Persistence{
						Size:       "1Gi",
						AccessMode: "ReadWriteOnce",
					},
					Datastore: operatorv1alpha1.DataStore{
						DatabaseType:     "sqlite3",
						ConnectionString: "/run/spire/data/datastore.sqlite3",
						MaxOpenConns:     100,
						MaxIdleConns:     2,
						ConnMaxLifetime:  0,
					},
					Federation: &operatorv1alpha1.FederationConfig{
						BundleEndpoint: operatorv1alpha1.BundleEndpointConfig{
							Profile:     operatorv1alpha1.HttpsSpiffeProfile,
							RefreshHint: 300,
						},
						ManagedRoute: "true",
						FederatesWith: []operatorv1alpha1.FederatesWithConfig{
							{
								TrustDomain:           fmt.Sprintf("%s.remote", appDomain),
								BundleEndpointUrl:     fmt.Sprintf("https://spire-server-federation.%s.remote", appDomain),
								BundleEndpointProfile: operatorv1alpha1.HttpsSpiffeProfile,
								EndpointSpiffeId:      fmt.Sprintf("spiffe://%s.remote/spire/server", appDomain),
							},
						},
					},
				},
			}
			err := k8sClient.Create(testCtx, server)
			Expect(err).NotTo(HaveOccurred(), "failed to create SpireServer with federation")

			By("Waiting for SpireServer conditions to stabilize")
			Eventually(func() bool {
				updatedServer := &operatorv1alpha1.SpireServer{}
				if err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, updatedServer); err != nil {
					return false
				}
				return len(updatedServer.Status.ConditionalStatus.Conditions) > 0
			}).WithTimeout(utils.DefaultTimeout).WithPolling(utils.DefaultInterval).Should(BeTrue(),
				"SpireServer conditions should be populated")
		})

		// Diff-suggested: New federation Service reconciliation added in federation.go
		It("Should create federation Service on port 8443", func() {
			By("Getting the federation Service")
			svc := &corev1.Service{}
			err := k8sClient.Get(testCtx, types.NamespacedName{
				Name:      "spire-server-federation",
				Namespace: utils.OperatorNamespace,
			}, svc)
			Expect(err).NotTo(HaveOccurred(), "federation Service should exist")

			By("Verifying Service port configuration")
			Expect(svc.Spec.Ports).To(HaveLen(1))
			Expect(svc.Spec.Ports[0].Port).To(Equal(int32(8443)))
			Expect(svc.Spec.Ports[0].Name).To(Equal("federation"))
			Expect(svc.Spec.Ports[0].Protocol).To(Equal(corev1.ProtocolTCP))

			By("Verifying Service labels")
			Expect(svc.Labels).To(HaveKeyWithValue("app.kubernetes.io/name", "spire-server"))
			Expect(svc.Labels).To(HaveKeyWithValue("app.kubernetes.io/managed-by", "zero-trust-workload-identity-manager"))

			By("Verifying service-ca TLS annotation")
			Expect(svc.Annotations).To(HaveKeyWithValue(
				"service.beta.openshift.io/serving-cert-secret-name",
				"spire-server-federation-tls",
			))

			By("Verifying Service selector")
			Expect(svc.Spec.Selector).To(HaveKeyWithValue("app.kubernetes.io/name", "spire-server"))
		})

		// Diff-suggested: New federation Route reconciliation added in federation.go
		It("Should create federation Route with TLS reencrypt", func() {
			By("Getting the federation Route")
			route := &routev1.Route{}
			Eventually(func() error {
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name:      "spire-server-federation",
					Namespace: utils.OperatorNamespace,
				}, route)
			}).WithTimeout(utils.ShortTimeout).WithPolling(utils.ShortInterval).Should(Succeed(),
				"federation Route should exist")

			By("Verifying Route TLS configuration")
			Expect(route.Spec.TLS).NotTo(BeNil())
			Expect(route.Spec.TLS.Termination).To(Equal(routev1.TLSTerminationReencrypt))

			By("Verifying Route target service")
			Expect(route.Spec.To.Kind).To(Equal("Service"))
			Expect(route.Spec.To.Name).To(Equal("spire-server-federation"))
		})

		// Diff-suggested: Federation config injection into configmap.go
		It("Should inject federation config into SPIRE server ConfigMap", func() {
			By("Getting the spire-server ConfigMap")
			cm := &corev1.ConfigMap{}
			err := k8sClient.Get(testCtx, types.NamespacedName{
				Name:      "spire-server",
				Namespace: utils.OperatorNamespace,
			}, cm)
			Expect(err).NotTo(HaveOccurred(), "spire-server ConfigMap should exist")

			By("Parsing server.conf JSON")
			serverConf := cm.Data["server.conf"]
			Expect(serverConf).NotTo(BeEmpty(), "server.conf should not be empty")

			var confMap map[string]interface{}
			err = json.Unmarshal([]byte(serverConf), &confMap)
			Expect(err).NotTo(HaveOccurred(), "server.conf should be valid JSON")

			By("Verifying federation bundle_endpoint is present in server section")
			serverSection, ok := confMap["server"].(map[string]interface{})
			Expect(ok).To(BeTrue(), "server section should exist")
			Expect(serverSection).To(HaveKey("bundle_endpoint"),
				"server section should contain bundle_endpoint")

			By("Verifying federates_with is present in server section")
			Expect(serverSection).To(HaveKey("federates_with"),
				"server section should contain federates_with")

			federatesWith, ok := serverSection["federates_with"].(map[string]interface{})
			Expect(ok).To(BeTrue(), "federates_with should be a map")
			Expect(federatesWith).To(HaveKey(fmt.Sprintf("%s.remote", appDomain)),
				"federates_with should contain the remote trust domain")
		})

		// Diff-suggested: Federation port added to statefulset.go
		It("Should add federation port to StatefulSet spire-server container", func() {
			By("Getting the spire-server StatefulSet")
			sts, err := clientset.AppsV1().StatefulSets(utils.OperatorNamespace).Get(
				testCtx, utils.SpireServerStatefulSetName, metav1.GetOptions{})
			Expect(err).NotTo(HaveOccurred(), "spire-server StatefulSet should exist")

			By("Finding the spire-server container")
			var spireServerContainer *corev1.Container
			for i := range sts.Spec.Template.Spec.Containers {
				if sts.Spec.Template.Spec.Containers[i].Name == "spire-server" {
					spireServerContainer = &sts.Spec.Template.Spec.Containers[i]
					break
				}
			}
			Expect(spireServerContainer).NotTo(BeNil(), "spire-server container should exist")

			By("Verifying federation port exists")
			hasFederationPort := false
			for _, port := range spireServerContainer.Ports {
				if port.Name == "federation" && port.ContainerPort == 8443 {
					hasFederationPort = true
					break
				}
			}
			Expect(hasFederationPort).To(BeTrue(),
				"spire-server container should have federation port 8443")
		})
	})

	// Diff-suggested: FederatesWith is dynamically updatable per EP-1863
	Context("FederatesWith dynamic updates", func() {
		It("Should allow adding new federated trust domains", func() {
			By("Getting current SpireServer")
			server := &operatorv1alpha1.SpireServer{}
			err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, server)
			if kerrors.IsNotFound(err) {
				Skip("SpireServer not found, skipping dynamic update test")
			}
			Expect(err).NotTo(HaveOccurred())

			if server.Spec.Federation == nil {
				Skip("Federation not configured, skipping dynamic update test")
			}

			By("Recording initial ConfigMap generation")
			initialCM := &corev1.ConfigMap{}
			err = k8sClient.Get(testCtx, types.NamespacedName{
				Name:      "spire-server",
				Namespace: utils.OperatorNamespace,
			}, initialCM)
			Expect(err).NotTo(HaveOccurred())
			initialConf := initialCM.Data["server.conf"]

			By("Adding a second federatesWith entry")
			err = utils.UpdateCRWithRetry(testCtx, k8sClient, server, func() {
				server.Spec.Federation.FederatesWith = append(
					server.Spec.Federation.FederatesWith,
					operatorv1alpha1.FederatesWithConfig{
						TrustDomain:           fmt.Sprintf("%s.remote2", appDomain),
						BundleEndpointUrl:     fmt.Sprintf("https://federation.%s.remote2", appDomain),
						BundleEndpointProfile: operatorv1alpha1.HttpsWebProfile,
					},
				)
			})
			Expect(err).NotTo(HaveOccurred(), "failed to update SpireServer with additional federatesWith")

			By("Waiting for ConfigMap to be updated with new trust domain")
			Eventually(func() bool {
				updatedCM := &corev1.ConfigMap{}
				if err := k8sClient.Get(testCtx, types.NamespacedName{
					Name:      "spire-server",
					Namespace: utils.OperatorNamespace,
				}, updatedCM); err != nil {
					return false
				}
				return updatedCM.Data["server.conf"] != initialConf
			}).WithTimeout(utils.DefaultTimeout).WithPolling(utils.DefaultInterval).Should(BeTrue(),
				"ConfigMap should be updated after adding federatesWith entry")

			By("Verifying both trust domains are in the ConfigMap")
			updatedCM := &corev1.ConfigMap{}
			err = k8sClient.Get(testCtx, types.NamespacedName{
				Name:      "spire-server",
				Namespace: utils.OperatorNamespace,
			}, updatedCM)
			Expect(err).NotTo(HaveOccurred())

			var confMap map[string]interface{}
			err = json.Unmarshal([]byte(updatedCM.Data["server.conf"]), &confMap)
			Expect(err).NotTo(HaveOccurred())

			serverSection := confMap["server"].(map[string]interface{})
			federatesWith := serverSection["federates_with"].(map[string]interface{})
			Expect(federatesWith).To(HaveKey(fmt.Sprintf("%s.remote", appDomain)))
			Expect(federatesWith).To(HaveKey(fmt.Sprintf("%s.remote2", appDomain)))
		})
	})

	// Diff-suggested: managedRoute field controls Route creation/deletion
	Context("Managed Route toggle", func() {
		It("Should remove Route when managedRoute is set to false", func() {
			By("Getting current SpireServer")
			server := &operatorv1alpha1.SpireServer{}
			err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, server)
			if kerrors.IsNotFound(err) {
				Skip("SpireServer not found, skipping managed route test")
			}
			Expect(err).NotTo(HaveOccurred())

			if server.Spec.Federation == nil {
				Skip("Federation not configured, skipping managed route test")
			}

			By("Setting managedRoute to false")
			err = utils.UpdateCRWithRetry(testCtx, k8sClient, server, func() {
				server.Spec.Federation.ManagedRoute = "false"
			})
			Expect(err).NotTo(HaveOccurred(), "failed to update managedRoute to false")

			By("Waiting for Route to be removed")
			Eventually(func() bool {
				route := &routev1.Route{}
				err := k8sClient.Get(testCtx, types.NamespacedName{
					Name:      "spire-server-federation",
					Namespace: utils.OperatorNamespace,
				}, route)
				return kerrors.IsNotFound(err)
			}).WithTimeout(utils.DefaultTimeout).WithPolling(utils.DefaultInterval).Should(BeTrue(),
				"federation Route should be removed when managedRoute is false")

			By("Re-enabling managedRoute")
			err = utils.UpdateCRWithRetry(testCtx, k8sClient, server, func() {
				server.Spec.Federation.ManagedRoute = "true"
			})
			Expect(err).NotTo(HaveOccurred(), "failed to re-enable managedRoute")

			By("Waiting for Route to be recreated")
			Eventually(func() error {
				route := &routev1.Route{}
				return k8sClient.Get(testCtx, types.NamespacedName{
					Name:      "spire-server-federation",
					Namespace: utils.OperatorNamespace,
				}, route)
			}).WithTimeout(utils.DefaultTimeout).WithPolling(utils.DefaultInterval).Should(Succeed(),
				"federation Route should be recreated when managedRoute is true")
		})
	})

	// Diff-suggested: CEL validation rules prevent federation removal and profile change
	Context("Federation immutability validation", func() {
		It("Should reject removing federation field once configured", func() {
			By("Getting current SpireServer")
			server := &operatorv1alpha1.SpireServer{}
			err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, server)
			if kerrors.IsNotFound(err) {
				Skip("SpireServer not found, skipping immutability test")
			}
			Expect(err).NotTo(HaveOccurred())

			if server.Spec.Federation == nil {
				Skip("Federation not configured, skipping immutability test")
			}

			By("Attempting to remove federation field")
			patch := client.MergeFrom(server.DeepCopy())
			server.Spec.Federation = nil
			err = k8sClient.Patch(testCtx, server, patch)
			Expect(err).To(HaveOccurred(), "removing federation should be rejected")
			Expect(err.Error()).To(ContainSubstring("spec.federation cannot be removed once configured"))
		})

		It("Should reject changing bundle endpoint profile", func() {
			By("Getting current SpireServer")
			server := &operatorv1alpha1.SpireServer{}
			err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, server)
			if kerrors.IsNotFound(err) {
				Skip("SpireServer not found, skipping profile immutability test")
			}
			Expect(err).NotTo(HaveOccurred())

			if server.Spec.Federation == nil {
				Skip("Federation not configured, skipping profile immutability test")
			}

			By("Attempting to change bundle endpoint profile from https_spiffe to https_web")
			patch := client.MergeFrom(server.DeepCopy())
			server.Spec.Federation.BundleEndpoint.Profile = operatorv1alpha1.HttpsWebProfile
			server.Spec.Federation.BundleEndpoint.HttpsWeb = &operatorv1alpha1.HttpsWebConfig{
				ServingCert: &operatorv1alpha1.ServingCertConfig{
					FileSyncInterval: 86400,
				},
			}
			err = k8sClient.Patch(testCtx, server, patch)
			Expect(err).To(HaveOccurred(), "changing profile should be rejected")
			Expect(err.Error()).To(ContainSubstring("profile is immutable"))
		})
	})

	// Diff-suggested: Federation Service not created when federation not configured
	Context("No federation configured", func() {
		It("Should not create federation resources when federation is not set", func() {
			By("Creating SpireServer without federation")
			server := &operatorv1alpha1.SpireServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: operatorv1alpha1.SpireServerSpec{
					JwtIssuer: jwtIssuer,
					CASubject: operatorv1alpha1.CASubject{
						Country: "US",
					},
					Persistence: operatorv1alpha1.Persistence{
						Size:       "1Gi",
						AccessMode: "ReadWriteOnce",
					},
					Datastore: operatorv1alpha1.DataStore{
						DatabaseType:     "sqlite3",
						ConnectionString: "/run/spire/data/datastore.sqlite3",
						MaxOpenConns:     100,
						MaxIdleConns:     2,
						ConnMaxLifetime:  0,
					},
				},
			}

			// Only create if it doesn't exist
			existing := &operatorv1alpha1.SpireServer{}
			err := k8sClient.Get(testCtx, types.NamespacedName{Name: "cluster"}, existing)
			if kerrors.IsNotFound(err) {
				err = k8sClient.Create(testCtx, server)
				Expect(err).NotTo(HaveOccurred(), "failed to create SpireServer without federation")
				DeferCleanup(func() {
					k8sClient.Delete(context.Background(), server)
				})
			} else if err == nil && existing.Spec.Federation != nil {
				Skip("SpireServer already exists with federation configured, skipping no-federation test")
			}

			By("Verifying federation Service does not exist")
			svc := &corev1.Service{}
			err = k8sClient.Get(testCtx, types.NamespacedName{
				Name:      "spire-server-federation",
				Namespace: utils.OperatorNamespace,
			}, svc)
			Expect(kerrors.IsNotFound(err)).To(BeTrue(),
				"federation Service should not exist when federation is not configured")

			By("Verifying federation Route does not exist")
			route := &routev1.Route{}
			err = k8sClient.Get(testCtx, types.NamespacedName{
				Name:      "spire-server-federation",
				Namespace: utils.OperatorNamespace,
			}, route)
			Expect(kerrors.IsNotFound(err)).To(BeTrue(),
				"federation Route should not exist when federation is not configured")
		})
	})
})
