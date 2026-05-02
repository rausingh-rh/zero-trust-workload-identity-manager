package spire_server

import (
	"reflect"
	"testing"

	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/zero-trust-workload-identity-manager/api/v1alpha1"
	"github.com/openshift/zero-trust-workload-identity-manager/pkg/controller/utils"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func TestGenerateFederationRoute(t *testing.T) {
	t.Run("generates Route with https_spiffe profile (passthrough TLS)", func(t *testing.T) {
		server := &v1alpha1.SpireServer{
			Spec: v1alpha1.SpireServerSpec{
				Federation: &v1alpha1.FederationConfig{
					BundleEndpoint: v1alpha1.BundleEndpointConfig{
						Profile: v1alpha1.HttpsSpiffeProfile,
					},
					ManagedRoute: "true",
				},
			},
		}

		route := generateFederationRoute(server)

		if route == nil {
			t.Fatal("Expected Route, got nil")
		}

		// Verify ObjectMeta
		if route.Name != "spire-server-federation" {
			t.Errorf("Expected Route name 'spire-server-federation', got %q", route.Name)
		}

		if route.Namespace != utils.GetOperatorNamespace() {
			t.Errorf("Expected namespace %q, got %q", utils.GetOperatorNamespace(), route.Namespace)
		}

		// Verify labels
		expectedLabels := utils.SpireServerLabels(nil)
		if !reflect.DeepEqual(route.Labels, expectedLabels) {
			t.Errorf("Expected labels %v, got %v", expectedLabels, route.Labels)
		}

		// Verify Port
		if route.Spec.Port == nil {
			t.Fatal("Expected Port to be set")
		}
		if route.Spec.Port.TargetPort != intstr.FromString("federation") {
			t.Errorf("Expected TargetPort 'federation', got %v", route.Spec.Port.TargetPort)
		}

		// Verify TLS - should be passthrough for https_spiffe
		if route.Spec.TLS == nil {
			t.Fatal("Expected TLS to be set")
		}
		if route.Spec.TLS.Termination != routev1.TLSTerminationPassthrough {
			t.Errorf("Expected TLS termination %q, got %q", routev1.TLSTerminationPassthrough, route.Spec.TLS.Termination)
		}
		if route.Spec.TLS.InsecureEdgeTerminationPolicy != routev1.InsecureEdgeTerminationPolicyNone {
			t.Errorf("Expected InsecureEdgeTerminationPolicy %q, got %q",
				routev1.InsecureEdgeTerminationPolicyNone, route.Spec.TLS.InsecureEdgeTerminationPolicy)
		}
		if route.Spec.TLS.ExternalCertificate != nil {
			t.Errorf("Expected ExternalCertificate to be nil for https_spiffe, got %v", route.Spec.TLS.ExternalCertificate)
		}

		// Verify To
		if route.Spec.To.Kind != "Service" {
			t.Errorf("Expected To.Kind 'Service', got %q", route.Spec.To.Kind)
		}
		if route.Spec.To.Name != "spire-server-federation" {
			t.Errorf("Expected To.Name 'spire-server-federation', got %q", route.Spec.To.Name)
		}
		if route.Spec.To.Weight == nil || *route.Spec.To.Weight != 100 {
			t.Error("Expected To.Weight to be 100")
		}

		// Verify WildcardPolicy
		if route.Spec.WildcardPolicy != routev1.WildcardPolicyNone {
			t.Errorf("Expected WildcardPolicy %q, got %q", routev1.WildcardPolicyNone, route.Spec.WildcardPolicy)
		}
	})

	t.Run("generates Route with https_web + servingCert (reencrypt TLS with ExternalCertificate)", func(t *testing.T) {
		server := &v1alpha1.SpireServer{
			Spec: v1alpha1.SpireServerSpec{
				Federation: &v1alpha1.FederationConfig{
					BundleEndpoint: v1alpha1.BundleEndpointConfig{
						Profile: v1alpha1.HttpsWebProfile,
						HttpsWeb: &v1alpha1.HttpsWebConfig{
							ServingCert: &v1alpha1.ServingCertConfig{
								ExternalSecretRef: "my-external-tls-secret",
							},
						},
					},
					ManagedRoute: "true",
				},
			},
		}

		route := generateFederationRoute(server)

		if route == nil {
			t.Fatal("Expected Route, got nil")
		}

		// Verify TLS - should be reencrypt with ExternalCertificate for servingCert
		if route.Spec.TLS == nil {
			t.Fatal("Expected TLS to be set")
		}
		if route.Spec.TLS.Termination != routev1.TLSTerminationReencrypt {
			t.Errorf("Expected TLS termination %q, got %q", routev1.TLSTerminationReencrypt, route.Spec.TLS.Termination)
		}
		if route.Spec.TLS.InsecureEdgeTerminationPolicy != routev1.InsecureEdgeTerminationPolicyRedirect {
			t.Errorf("Expected InsecureEdgeTerminationPolicy %q, got %q",
				routev1.InsecureEdgeTerminationPolicyRedirect, route.Spec.TLS.InsecureEdgeTerminationPolicy)
		}
		if route.Spec.TLS.ExternalCertificate == nil {
			t.Fatal("Expected ExternalCertificate to be set for servingCert config")
		}
		if route.Spec.TLS.ExternalCertificate.Name != "my-external-tls-secret" {
			t.Errorf("Expected ExternalCertificate name 'my-external-tls-secret', got %q",
				route.Spec.TLS.ExternalCertificate.Name)
		}
	})

	t.Run("generates Route with https_web + servingCert without externalSecretRef (passthrough TLS)", func(t *testing.T) {
		server := &v1alpha1.SpireServer{
			Spec: v1alpha1.SpireServerSpec{
				Federation: &v1alpha1.FederationConfig{
					BundleEndpoint: v1alpha1.BundleEndpointConfig{
						Profile: v1alpha1.HttpsWebProfile,
						HttpsWeb: &v1alpha1.HttpsWebConfig{
							ServingCert: &v1alpha1.ServingCertConfig{
								ExternalSecretRef: "", // Empty - no external cert
							},
						},
					},
					ManagedRoute: "true",
				},
			},
		}

		route := generateFederationRoute(server)

		if route == nil {
			t.Fatal("Expected Route, got nil")
		}

		// Should stay as passthrough when no externalSecretRef is provided
		if route.Spec.TLS.Termination != routev1.TLSTerminationPassthrough {
			t.Errorf("Expected TLS termination %q when no externalSecretRef, got %q",
				routev1.TLSTerminationPassthrough, route.Spec.TLS.Termination)
		}
		if route.Spec.TLS.ExternalCertificate != nil {
			t.Errorf("Expected ExternalCertificate to be nil when externalSecretRef is empty")
		}
	})

	t.Run("generates Route with https_web + ACME (passthrough TLS)", func(t *testing.T) {
		server := &v1alpha1.SpireServer{
			Spec: v1alpha1.SpireServerSpec{
				Federation: &v1alpha1.FederationConfig{
					BundleEndpoint: v1alpha1.BundleEndpointConfig{
						Profile: v1alpha1.HttpsWebProfile,
						HttpsWeb: &v1alpha1.HttpsWebConfig{
							Acme: &v1alpha1.AcmeConfig{
								DirectoryUrl: "https://acme-v02.api.letsencrypt.org/directory",
								DomainName:   "federation.example.com",
								Email:        "admin@example.com",
								TosAccepted:  "true",
							},
						},
					},
					ManagedRoute: "true",
				},
			},
		}

		route := generateFederationRoute(server)

		if route == nil {
			t.Fatal("Expected Route, got nil")
		}

		// ACME does not use ExternalCertificate - should be passthrough
		if route.Spec.TLS.Termination != routev1.TLSTerminationPassthrough {
			t.Errorf("Expected TLS termination %q for ACME profile, got %q",
				routev1.TLSTerminationPassthrough, route.Spec.TLS.Termination)
		}
		if route.Spec.TLS.ExternalCertificate != nil {
			t.Errorf("Expected ExternalCertificate to be nil for ACME profile")
		}
	})

	t.Run("generates Route with custom labels", func(t *testing.T) {
		customLabels := map[string]string{
			"env":      "production",
			"priority": "high",
		}

		server := &v1alpha1.SpireServer{
			Spec: v1alpha1.SpireServerSpec{
				CommonConfig: v1alpha1.CommonConfig{
					Labels: customLabels,
				},
				Federation: &v1alpha1.FederationConfig{
					BundleEndpoint: v1alpha1.BundleEndpointConfig{
						Profile: v1alpha1.HttpsSpiffeProfile,
					},
					ManagedRoute: "true",
				},
			},
		}

		route := generateFederationRoute(server)

		if route == nil {
			t.Fatal("Expected Route, got nil")
		}

		// Verify custom labels are present
		if val, ok := route.Labels["env"]; !ok || val != "production" {
			t.Errorf("Expected custom label 'env=production', got %q", val)
		}
		if val, ok := route.Labels["priority"]; !ok || val != "high" {
			t.Errorf("Expected custom label 'priority=high', got %q", val)
		}

		// Verify standard labels are still present
		if val, ok := route.Labels[utils.AppManagedByLabelKey]; !ok || val != utils.AppManagedByLabelValue {
			t.Errorf("Expected label %s=%s to be preserved with custom labels",
				utils.AppManagedByLabelKey, utils.AppManagedByLabelValue)
		}

		if val, ok := route.Labels["app.kubernetes.io/component"]; !ok || val != utils.ComponentControlPlane {
			t.Errorf("Expected label app.kubernetes.io/component=%s to be preserved with custom labels",
				utils.ComponentControlPlane)
		}
	})

	t.Run("handles nil labels gracefully", func(t *testing.T) {
		server := &v1alpha1.SpireServer{
			Spec: v1alpha1.SpireServerSpec{
				CommonConfig: v1alpha1.CommonConfig{
					Labels: nil,
				},
				Federation: &v1alpha1.FederationConfig{
					BundleEndpoint: v1alpha1.BundleEndpointConfig{
						Profile: v1alpha1.HttpsSpiffeProfile,
					},
					ManagedRoute: "true",
				},
			},
		}

		route := generateFederationRoute(server)

		if route == nil {
			t.Fatal("Expected Route, got nil")
		}

		// Should only have default labels
		expectedLabels := utils.SpireServerLabels(nil)
		if !reflect.DeepEqual(route.Labels, expectedLabels) {
			t.Errorf("Expected default labels %v, got %v", expectedLabels, route.Labels)
		}
	})

	t.Run("returns consistent results across multiple calls", func(t *testing.T) {
		server := &v1alpha1.SpireServer{
			Spec: v1alpha1.SpireServerSpec{
				Federation: &v1alpha1.FederationConfig{
					BundleEndpoint: v1alpha1.BundleEndpointConfig{
						Profile: v1alpha1.HttpsSpiffeProfile,
					},
					ManagedRoute: "true",
				},
			},
		}

		result1 := generateFederationRoute(server)
		result2 := generateFederationRoute(server)

		if result1.Name != result2.Name {
			t.Errorf("Inconsistent name: %q vs %q", result1.Name, result2.Name)
		}
		if result1.Namespace != result2.Namespace {
			t.Errorf("Inconsistent namespace: %q vs %q", result1.Namespace, result2.Namespace)
		}
		if !reflect.DeepEqual(result1.Labels, result2.Labels) {
			t.Errorf("Inconsistent labels: %v vs %v", result1.Labels, result2.Labels)
		}
		if !reflect.DeepEqual(result1.Spec, result2.Spec) {
			t.Error("Inconsistent spec across multiple calls")
		}
	})
}

func TestGenerateFederationRoute_TLSConfiguration(t *testing.T) {
	tests := []struct {
		name                        string
		profile                     v1alpha1.BundleEndpointProfile
		httpsWeb                    *v1alpha1.HttpsWebConfig
		expectedTermination         routev1.TLSTerminationType
		expectedInsecurePolicy      routev1.InsecureEdgeTerminationPolicyType
		expectedExternalCert        bool
		expectedExternalCertName    string
	}{
		{
			name:                   "https_spiffe profile uses passthrough TLS",
			profile:                v1alpha1.HttpsSpiffeProfile,
			httpsWeb:               nil,
			expectedTermination:    routev1.TLSTerminationPassthrough,
			expectedInsecurePolicy: routev1.InsecureEdgeTerminationPolicyNone,
			expectedExternalCert:   false,
		},
		{
			name:    "https_web with ACME uses passthrough TLS",
			profile: v1alpha1.HttpsWebProfile,
			httpsWeb: &v1alpha1.HttpsWebConfig{
				Acme: &v1alpha1.AcmeConfig{
					DirectoryUrl: "https://acme.example.com/directory",
					DomainName:   "federation.example.com",
					Email:        "admin@example.com",
				},
			},
			expectedTermination:    routev1.TLSTerminationPassthrough,
			expectedInsecurePolicy: routev1.InsecureEdgeTerminationPolicyNone,
			expectedExternalCert:   false,
		},
		{
			name:    "https_web with servingCert and externalSecretRef uses reencrypt TLS",
			profile: v1alpha1.HttpsWebProfile,
			httpsWeb: &v1alpha1.HttpsWebConfig{
				ServingCert: &v1alpha1.ServingCertConfig{
					ExternalSecretRef: "tls-secret",
				},
			},
			expectedTermination:      routev1.TLSTerminationReencrypt,
			expectedInsecurePolicy:   routev1.InsecureEdgeTerminationPolicyRedirect,
			expectedExternalCert:     true,
			expectedExternalCertName: "tls-secret",
		},
		{
			name:    "https_web with servingCert but empty externalSecretRef uses passthrough TLS",
			profile: v1alpha1.HttpsWebProfile,
			httpsWeb: &v1alpha1.HttpsWebConfig{
				ServingCert: &v1alpha1.ServingCertConfig{
					ExternalSecretRef: "",
				},
			},
			expectedTermination:    routev1.TLSTerminationPassthrough,
			expectedInsecurePolicy: routev1.InsecureEdgeTerminationPolicyNone,
			expectedExternalCert:   false,
		},
		{
			name:    "https_web with nil httpsWeb uses passthrough TLS",
			profile: v1alpha1.HttpsWebProfile,
			httpsWeb: nil,
			expectedTermination:    routev1.TLSTerminationPassthrough,
			expectedInsecurePolicy: routev1.InsecureEdgeTerminationPolicyNone,
			expectedExternalCert:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &v1alpha1.SpireServer{
				Spec: v1alpha1.SpireServerSpec{
					Federation: &v1alpha1.FederationConfig{
						BundleEndpoint: v1alpha1.BundleEndpointConfig{
							Profile:  tt.profile,
							HttpsWeb: tt.httpsWeb,
						},
						ManagedRoute: "true",
					},
				},
			}

			route := generateFederationRoute(server)

			if route.Spec.TLS.Termination != tt.expectedTermination {
				t.Errorf("Expected TLS termination %q, got %q", tt.expectedTermination, route.Spec.TLS.Termination)
			}

			if route.Spec.TLS.InsecureEdgeTerminationPolicy != tt.expectedInsecurePolicy {
				t.Errorf("Expected InsecureEdgeTerminationPolicy %q, got %q",
					tt.expectedInsecurePolicy, route.Spec.TLS.InsecureEdgeTerminationPolicy)
			}

			if tt.expectedExternalCert {
				if route.Spec.TLS.ExternalCertificate == nil {
					t.Fatal("Expected ExternalCertificate to be set")
				}
				if route.Spec.TLS.ExternalCertificate.Name != tt.expectedExternalCertName {
					t.Errorf("Expected ExternalCertificate name %q, got %q",
						tt.expectedExternalCertName, route.Spec.TLS.ExternalCertificate.Name)
				}
			} else {
				if route.Spec.TLS.ExternalCertificate != nil {
					t.Errorf("Expected ExternalCertificate to be nil, got %v", route.Spec.TLS.ExternalCertificate)
				}
			}
		})
	}
}

func TestCheckFederationRouteConflict(t *testing.T) {
	t.Run("returns false when routes are identical", func(t *testing.T) {
		current := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{"app": "spire-server"},
			},
			Spec: routev1.RouteSpec{
				Port: &routev1.RoutePort{
					TargetPort: intstr.FromString("federation"),
				},
				TLS: &routev1.TLSConfig{
					Termination:                   routev1.TLSTerminationPassthrough,
					InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
				},
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: "spire-server-federation",
				},
				WildcardPolicy: routev1.WildcardPolicyNone,
			},
		}

		desired := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{"app": "spire-server"},
			},
			Spec: routev1.RouteSpec{
				Port: &routev1.RoutePort{
					TargetPort: intstr.FromString("federation"),
				},
				TLS: &routev1.TLSConfig{
					Termination:                   routev1.TLSTerminationPassthrough,
					InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
				},
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: "spire-server-federation",
				},
				WildcardPolicy: routev1.WildcardPolicyNone,
			},
		}

		result := checkFederationRouteConflict(current, desired)
		if result {
			t.Error("Expected no conflict for identical routes, but conflict was detected")
		}
	})

	t.Run("returns true when TLS termination differs", func(t *testing.T) {
		current := &routev1.Route{
			Spec: routev1.RouteSpec{
				TLS: &routev1.TLSConfig{
					Termination: routev1.TLSTerminationPassthrough,
				},
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: "spire-server-federation",
				},
			},
		}

		desired := &routev1.Route{
			Spec: routev1.RouteSpec{
				TLS: &routev1.TLSConfig{
					Termination: routev1.TLSTerminationReencrypt,
				},
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: "spire-server-federation",
				},
			},
		}

		result := checkFederationRouteConflict(current, desired)
		if !result {
			t.Error("Expected conflict when TLS termination differs, but no conflict was detected")
		}
	})

	t.Run("returns true when target service name differs", func(t *testing.T) {
		current := &routev1.Route{
			Spec: routev1.RouteSpec{
				TLS: &routev1.TLSConfig{
					Termination: routev1.TLSTerminationPassthrough,
				},
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: "old-service",
				},
			},
		}

		desired := &routev1.Route{
			Spec: routev1.RouteSpec{
				TLS: &routev1.TLSConfig{
					Termination: routev1.TLSTerminationPassthrough,
				},
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: "spire-server-federation",
				},
			},
		}

		result := checkFederationRouteConflict(current, desired)
		if !result {
			t.Error("Expected conflict when target service name differs, but no conflict was detected")
		}
	})

	t.Run("returns true when labels differ", func(t *testing.T) {
		current := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{"version": "v1"},
			},
			Spec: routev1.RouteSpec{
				TLS: &routev1.TLSConfig{
					Termination: routev1.TLSTerminationPassthrough,
				},
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: "spire-server-federation",
				},
			},
		}

		desired := &routev1.Route{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{"version": "v2"},
			},
			Spec: routev1.RouteSpec{
				TLS: &routev1.TLSConfig{
					Termination: routev1.TLSTerminationPassthrough,
				},
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: "spire-server-federation",
				},
			},
		}

		result := checkFederationRouteConflict(current, desired)
		if !result {
			t.Error("Expected conflict when labels differ, but no conflict was detected")
		}
	})

	t.Run("returns true when ExternalCertificate added", func(t *testing.T) {
		current := &routev1.Route{
			Spec: routev1.RouteSpec{
				TLS: &routev1.TLSConfig{
					Termination: routev1.TLSTerminationPassthrough,
				},
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: "spire-server-federation",
				},
			},
		}

		desired := &routev1.Route{
			Spec: routev1.RouteSpec{
				TLS: &routev1.TLSConfig{
					Termination: routev1.TLSTerminationReencrypt,
					ExternalCertificate: &routev1.LocalObjectReference{
						Name: "my-tls-secret",
					},
				},
				To: routev1.RouteTargetReference{
					Kind: "Service",
					Name: "spire-server-federation",
				},
			},
		}

		result := checkFederationRouteConflict(current, desired)
		if !result {
			t.Error("Expected conflict when ExternalCertificate is added, but no conflict was detected")
		}
	})
}

func TestCheckFederationRouteConflict_TableDriven(t *testing.T) {
	weight100 := int32(100)
	weight50 := int32(50)

	tests := []struct {
		name             string
		current          *routev1.Route
		desired          *routev1.Route
		expectedConflict bool
	}{
		{
			name: "no conflict - identical routes with passthrough TLS",
			current: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "spire"}},
				Spec: routev1.RouteSpec{
					Port: &routev1.RoutePort{TargetPort: intstr.FromString("federation")},
					TLS: &routev1.TLSConfig{
						Termination:                   routev1.TLSTerminationPassthrough,
						InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
					},
					To:             routev1.RouteTargetReference{Kind: "Service", Name: "spire-server-federation", Weight: &weight100},
					WildcardPolicy: routev1.WildcardPolicyNone,
				},
			},
			desired: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "spire"}},
				Spec: routev1.RouteSpec{
					Port: &routev1.RoutePort{TargetPort: intstr.FromString("federation")},
					TLS: &routev1.TLSConfig{
						Termination:                   routev1.TLSTerminationPassthrough,
						InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
					},
					To:             routev1.RouteTargetReference{Kind: "Service", Name: "spire-server-federation", Weight: &weight100},
					WildcardPolicy: routev1.WildcardPolicyNone,
				},
			},
			expectedConflict: false,
		},
		{
			name: "conflict - TLS termination changed from passthrough to reencrypt",
			current: &routev1.Route{
				Spec: routev1.RouteSpec{
					TLS: &routev1.TLSConfig{Termination: routev1.TLSTerminationPassthrough},
					To:  routev1.RouteTargetReference{Kind: "Service", Name: "spire-server-federation"},
				},
			},
			desired: &routev1.Route{
				Spec: routev1.RouteSpec{
					TLS: &routev1.TLSConfig{Termination: routev1.TLSTerminationReencrypt},
					To:  routev1.RouteTargetReference{Kind: "Service", Name: "spire-server-federation"},
				},
			},
			expectedConflict: true,
		},
		{
			name: "conflict - weight differs",
			current: &routev1.Route{
				Spec: routev1.RouteSpec{
					TLS: &routev1.TLSConfig{Termination: routev1.TLSTerminationPassthrough},
					To:  routev1.RouteTargetReference{Kind: "Service", Name: "spire-server-federation", Weight: &weight100},
				},
			},
			desired: &routev1.Route{
				Spec: routev1.RouteSpec{
					TLS: &routev1.TLSConfig{Termination: routev1.TLSTerminationPassthrough},
					To:  routev1.RouteTargetReference{Kind: "Service", Name: "spire-server-federation", Weight: &weight50},
				},
			},
			expectedConflict: true,
		},
		{
			name: "conflict - port target differs",
			current: &routev1.Route{
				Spec: routev1.RouteSpec{
					Port: &routev1.RoutePort{TargetPort: intstr.FromString("grpc")},
					TLS:  &routev1.TLSConfig{Termination: routev1.TLSTerminationPassthrough},
					To:   routev1.RouteTargetReference{Kind: "Service", Name: "spire-server-federation"},
				},
			},
			desired: &routev1.Route{
				Spec: routev1.RouteSpec{
					Port: &routev1.RoutePort{TargetPort: intstr.FromString("federation")},
					TLS:  &routev1.TLSConfig{Termination: routev1.TLSTerminationPassthrough},
					To:   routev1.RouteTargetReference{Kind: "Service", Name: "spire-server-federation"},
				},
			},
			expectedConflict: true,
		},
		{
			name: "conflict - labels added",
			current: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "spire"}},
				Spec: routev1.RouteSpec{
					TLS: &routev1.TLSConfig{Termination: routev1.TLSTerminationPassthrough},
					To:  routev1.RouteTargetReference{Kind: "Service", Name: "spire-server-federation"},
				},
			},
			desired: &routev1.Route{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "spire", "env": "prod"}},
				Spec: routev1.RouteSpec{
					TLS: &routev1.TLSConfig{Termination: routev1.TLSTerminationPassthrough},
					To:  routev1.RouteTargetReference{Kind: "Service", Name: "spire-server-federation"},
				},
			},
			expectedConflict: true,
		},
		{
			name: "no conflict - both have nil labels",
			current: &routev1.Route{
				Spec: routev1.RouteSpec{
					TLS: &routev1.TLSConfig{Termination: routev1.TLSTerminationPassthrough},
					To:  routev1.RouteTargetReference{Kind: "Service", Name: "spire-server-federation"},
				},
			},
			desired: &routev1.Route{
				Spec: routev1.RouteSpec{
					TLS: &routev1.TLSConfig{Termination: routev1.TLSTerminationPassthrough},
					To:  routev1.RouteTargetReference{Kind: "Service", Name: "spire-server-federation"},
				},
			},
			expectedConflict: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := checkFederationRouteConflict(tt.current, tt.desired)
			if result != tt.expectedConflict {
				t.Errorf("Expected conflict=%v, got %v", tt.expectedConflict, result)
			}
		})
	}
}
