package spire_server

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	routev1 "github.com/openshift/api/route/v1"
	"github.com/openshift/zero-trust-workload-identity-manager/api/v1alpha1"
	"github.com/openshift/zero-trust-workload-identity-manager/pkg/controller/status"
	"github.com/openshift/zero-trust-workload-identity-manager/pkg/controller/utils"
)

const (
	// Federation condition types
	FederationServiceAvailable = "FederationServiceAvailable"
	FederationRouteAvailable   = "FederationRouteAvailable"
	FederationConfigValid      = "FederationConfigValid"

	// Federation resource names
	spireFederationServiceName = "spire-server-federation"
	spireFederationRouteName   = "spire-server-federation"

	// Federation bundle endpoint port
	federationBundleEndpointPort = 8443
)

// reconcileFederation reconciles all federation-related resources:
// the federation Service, the federation Route (if managedRoute is enabled),
// and validates the federation configuration.
func (r *SpireServerReconciler) reconcileFederation(ctx context.Context, server *v1alpha1.SpireServer, statusMgr *status.Manager, createOnlyMode bool) error {
	if server.Spec.Federation == nil {
		r.log.V(1).Info("Federation is not configured, skipping federation reconciliation")
		return nil
	}

	r.log.Info("Reconciling federation resources")

	// Validate federation configuration
	if err := r.validateFederationConfig(server, statusMgr); err != nil {
		return err
	}

	// Reconcile federation Service
	if err := r.reconcileFederationService(ctx, server, statusMgr, createOnlyMode); err != nil {
		return err
	}

	// Reconcile federation Route (conditionally based on managedRoute)
	if err := r.reconcileFederationRoute(ctx, server, statusMgr, createOnlyMode); err != nil {
		return err
	}

	return nil
}

// validateFederationConfig validates the federation configuration from the SpireServer spec
func (r *SpireServerReconciler) validateFederationConfig(server *v1alpha1.SpireServer, statusMgr *status.Manager) error {
	federation := server.Spec.Federation
	if federation == nil {
		return nil
	}

	// Validate ACME config if https_web with ACME is used
	if federation.BundleEndpoint.Profile == v1alpha1.HttpsWebProfile && federation.BundleEndpoint.HttpsWeb != nil {
		if federation.BundleEndpoint.HttpsWeb.Acme != nil {
			acme := federation.BundleEndpoint.HttpsWeb.Acme
			if err := utils.IsValidURL(acme.DirectoryUrl); err != nil {
				r.log.Error(err, "Invalid ACME directory URL", "directoryUrl", acme.DirectoryUrl)
				statusMgr.AddCondition(FederationConfigValid, "InvalidACMEDirectoryURL",
					fmt.Sprintf("ACME directory URL validation failed: %v", err),
					metav1.ConditionFalse)
				return fmt.Errorf("invalid ACME directory URL: %w", err)
			}
		}
	}

	// Validate federatesWith entries
	for i, fedWith := range federation.FederatesWith {
		if fedWith.BundleEndpointProfile == v1alpha1.HttpsSpiffeProfile && fedWith.EndpointSpiffeId == "" {
			err := fmt.Errorf("federatesWith[%d] with trustDomain %q: endpointSpiffeId is required when bundleEndpointProfile is https_spiffe", i, fedWith.TrustDomain)
			r.log.Error(err, "Invalid federatesWith configuration")
			statusMgr.AddCondition(FederationConfigValid, "InvalidFederatesWithConfig",
				err.Error(),
				metav1.ConditionFalse)
			return err
		}

		if err := utils.IsValidURL(fedWith.BundleEndpointUrl); err != nil {
			r.log.Error(err, "Invalid bundle endpoint URL for federatesWith", "trustDomain", fedWith.TrustDomain)
			statusMgr.AddCondition(FederationConfigValid, "InvalidFederatesWithURL",
				fmt.Sprintf("Bundle endpoint URL validation failed for trust domain %q: %v", fedWith.TrustDomain, err),
				metav1.ConditionFalse)
			return fmt.Errorf("invalid bundle endpoint URL for trust domain %q: %w", fedWith.TrustDomain, err)
		}
	}

	existingCondition := apimeta.FindStatusCondition(server.Status.ConditionalStatus.Conditions, FederationConfigValid)
	if existingCondition != nil && existingCondition.Status == metav1.ConditionFalse {
		statusMgr.AddCondition(FederationConfigValid, v1alpha1.ReasonReady,
			"Federation configuration validation passed",
			metav1.ConditionTrue)
	}

	return nil
}

// reconcileFederationService reconciles the federation bundle endpoint Service
func (r *SpireServerReconciler) reconcileFederationService(ctx context.Context, server *v1alpha1.SpireServer, statusMgr *status.Manager, createOnlyMode bool) error {
	desired := generateFederationService(server.Spec.Labels)

	// Get existing resource
	existing := &corev1.Service{}
	err := r.ctrlClient.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)

	if err != nil {
		if !kerrors.IsNotFound(err) {
			r.log.Error(err, "failed to get federation service")
			statusMgr.AddCondition(FederationServiceAvailable, v1alpha1.ReasonFailed,
				fmt.Sprintf("Failed to get federation Service: %v", err),
				metav1.ConditionFalse)
			return err
		}

		// Resource doesn't exist, create it
		if err := r.ctrlClient.Create(ctx, desired); err != nil {
			r.log.Error(err, "failed to create federation service")
			statusMgr.AddCondition(FederationServiceAvailable, v1alpha1.ReasonFailed,
				fmt.Sprintf("Failed to create federation Service: %v", err),
				metav1.ConditionFalse)
			return err
		}

		r.log.Info("Created federation Service", "name", desired.Name, "namespace", desired.Namespace)
		statusMgr.AddCondition(FederationServiceAvailable, v1alpha1.ReasonReady,
			"Federation Service created",
			metav1.ConditionTrue)
		return nil
	}

	// Resource exists, check if we need to update
	if createOnlyMode {
		r.log.V(1).Info("Federation Service exists, skipping update due to create-only mode", "name", desired.Name)
		return nil
	}

	// Preserve Kubernetes-managed fields from existing resource BEFORE comparison
	desired.ResourceVersion = existing.ResourceVersion
	desired.Spec.ClusterIP = existing.Spec.ClusterIP
	desired.Spec.ClusterIPs = existing.Spec.ClusterIPs
	desired.Spec.IPFamilies = existing.Spec.IPFamilies
	desired.Spec.IPFamilyPolicy = existing.Spec.IPFamilyPolicy
	desired.Spec.InternalTrafficPolicy = existing.Spec.InternalTrafficPolicy
	desired.Spec.SessionAffinity = existing.Spec.SessionAffinity
	if existing.Spec.HealthCheckNodePort != 0 {
		desired.Spec.HealthCheckNodePort = existing.Spec.HealthCheckNodePort
	}

	// Normalize ports
	for i := range desired.Spec.Ports {
		if desired.Spec.Ports[i].Protocol == "" {
			desired.Spec.Ports[i].Protocol = corev1.ProtocolTCP
		}
	}

	if !utils.ResourceNeedsUpdate(existing, desired) {
		r.log.V(1).Info("Federation Service is up to date", "name", desired.Name)
		return nil
	}

	if err := r.ctrlClient.Update(ctx, desired); err != nil {
		r.log.Error(err, "failed to update federation service")
		statusMgr.AddCondition(FederationServiceAvailable, v1alpha1.ReasonFailed,
			fmt.Sprintf("Failed to update federation Service: %v", err),
			metav1.ConditionFalse)
		return err
	}

	r.log.Info("Updated federation Service", "name", desired.Name, "namespace", desired.Namespace)
	statusMgr.AddCondition(FederationServiceAvailable, v1alpha1.ReasonReady,
		"Federation Service updated",
		metav1.ConditionTrue)
	return nil
}

// reconcileFederationRoute reconciles the federation Route based on managedRoute setting
func (r *SpireServerReconciler) reconcileFederationRoute(ctx context.Context, server *v1alpha1.SpireServer, statusMgr *status.Manager, createOnlyMode bool) error {
	federation := server.Spec.Federation

	if utils.StringToBool(federation.ManagedRoute) {
		route := generateFederationRoute(server)

		var existingRoute routev1.Route
		err := r.ctrlClient.Get(ctx, types.NamespacedName{
			Name:      route.Name,
			Namespace: route.Namespace,
		}, &existingRoute)

		if err != nil {
			if kerrors.IsNotFound(err) {
				if err = r.ctrlClient.Create(ctx, route); err != nil {
					r.log.Error(err, "failed to create federation route")
					statusMgr.AddCondition(FederationRouteAvailable, "FederationManagedRouteCreationFailed",
						err.Error(),
						metav1.ConditionFalse)
					return err
				}

				statusMgr.AddCondition(FederationRouteAvailable, "FederationManagedRouteCreated",
					"Federation Managed Route created",
					metav1.ConditionTrue)
				r.log.Info("Created federation route", "namespace", route.Namespace, "name", route.Name)
			} else {
				r.log.Error(err, "failed to get existing federation route")
				statusMgr.AddCondition(FederationRouteAvailable, "FederationManagedRouteRetrievalFailed",
					err.Error(),
					metav1.ConditionFalse)
				return err
			}
		} else if checkFederationRouteConflict(&existingRoute, route) {
			r.log.Info("Found conflict in federation routes, updating route")
			route.ResourceVersion = existingRoute.ResourceVersion

			if createOnlyMode {
				r.log.Info("Skipping federation Route update due to create-only mode", "namespace", route.Namespace, "name", route.Name)
			} else {
				err = r.ctrlClient.Update(ctx, route)
				if err != nil {
					statusMgr.AddCondition(FederationRouteAvailable, "FederationManagedRouteUpdateFailed",
						err.Error(),
						metav1.ConditionFalse)
					return err
				}

				statusMgr.AddCondition(FederationRouteAvailable, "FederationManagedRouteUpdated",
					"Federation Managed Route updated",
					metav1.ConditionTrue)
				r.log.Info("Updated federation route", "namespace", route.Namespace, "name", route.Name)
			}
		} else {
			existingCondition := apimeta.FindStatusCondition(server.Status.ConditionalStatus.Conditions, FederationRouteAvailable)
			if existingCondition == nil || existingCondition.Status != metav1.ConditionTrue {
				statusMgr.AddCondition(FederationRouteAvailable, "FederationManagedRouteReady",
					"Federation Managed Route is ready",
					metav1.ConditionTrue)
			}
		}
	} else {
		statusMgr.AddCondition(FederationRouteAvailable, "FederationManagedRouteDisabled",
			"Federation Managed Route disabled",
			metav1.ConditionFalse)
	}

	return nil
}

// checkFederationRouteConflict returns true if desired & current routes have conflicts
func checkFederationRouteConflict(current, desired *routev1.Route) bool {
	return !equality.Semantic.DeepEqual(current.Spec, desired.Spec) || !equality.Semantic.DeepEqual(current.Labels, desired.Labels)
}

// generateFederationService creates the federation bundle endpoint Service
func generateFederationService(customLabels map[string]string) *corev1.Service {
	labels := utils.SpireServerLabels(customLabels)

	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spireFederationServiceName,
			Namespace: utils.GetOperatorNamespace(),
			Labels:    labels,
			Annotations: map[string]string{
				"service.beta.openshift.io/serving-cert-secret-name": "spire-server-federation-tls",
			},
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:       "federation",
					Port:       int32(federationBundleEndpointPort),
					TargetPort: intstr.FromInt(federationBundleEndpointPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			Selector: map[string]string{
				"app.kubernetes.io/name":     "spire-server",
				"app.kubernetes.io/instance": utils.StandardInstance,
			},
		},
	}
}

// generateFederationRoute creates an OpenShift Route for the federation bundle endpoint
func generateFederationRoute(server *v1alpha1.SpireServer) *routev1.Route {
	labels := utils.SpireServerLabels(server.Spec.Labels)
	federation := server.Spec.Federation

	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      spireFederationRouteName,
			Namespace: utils.GetOperatorNamespace(),
			Labels:    labels,
		},
		Spec: routev1.RouteSpec{
			Port: &routev1.RoutePort{
				TargetPort: intstr.FromString("federation"),
			},
			TLS: &routev1.TLSConfig{
				Termination:                   routev1.TLSTerminationReencrypt,
				InsecureEdgeTerminationPolicy: routev1.InsecureEdgeTerminationPolicyNone,
			},
			To: routev1.RouteTargetReference{
				Kind:   "Service",
				Name:   spireFederationServiceName,
				Weight: &[]int32{100}[0],
			},
			WildcardPolicy: routev1.WildcardPolicyNone,
		},
	}

	// If https_web with servingCert and externalSecretRef, configure the Route TLS
	if federation.BundleEndpoint.Profile == v1alpha1.HttpsWebProfile &&
		federation.BundleEndpoint.HttpsWeb != nil &&
		federation.BundleEndpoint.HttpsWeb.ServingCert != nil &&
		federation.BundleEndpoint.HttpsWeb.ServingCert.ExternalSecretRef != "" {
		route.Spec.TLS.ExternalCertificate = &routev1.LocalObjectReference{
			Name: federation.BundleEndpoint.HttpsWeb.ServingCert.ExternalSecretRef,
		}
	}

	// If https_web with ACME, set the domain name as the Route host
	if federation.BundleEndpoint.Profile == v1alpha1.HttpsWebProfile &&
		federation.BundleEndpoint.HttpsWeb != nil &&
		federation.BundleEndpoint.HttpsWeb.Acme != nil {
		route.Spec.Host = federation.BundleEndpoint.HttpsWeb.Acme.DomainName
	}

	return route
}

// generateFederationServerConfig generates the federation section for the SPIRE server configuration.
// This is injected into the server.conf under the "server" section.
func generateFederationServerConfig(federation *v1alpha1.FederationConfig) map[string]interface{} {
	if federation == nil {
		return nil
	}

	bundleEndpoint := map[string]interface{}{
		"address": "0.0.0.0",
		"port":    federationBundleEndpointPort,
	}

	// Configure refresh_hint
	if federation.BundleEndpoint.RefreshHint > 0 {
		bundleEndpoint["refresh_hint"] = federation.BundleEndpoint.RefreshHint
	}

	// Configure profile
	profileConfig := map[string]interface{}{}
	switch federation.BundleEndpoint.Profile {
	case v1alpha1.HttpsSpiffeProfile:
		profileConfig["https_spiffe"] = map[string]interface{}{}
	case v1alpha1.HttpsWebProfile:
		webConfig := map[string]interface{}{}
		if federation.BundleEndpoint.HttpsWeb != nil {
			if federation.BundleEndpoint.HttpsWeb.Acme != nil {
				acmeConfig := map[string]interface{}{
					"directory_url": federation.BundleEndpoint.HttpsWeb.Acme.DirectoryUrl,
					"domain_name":   federation.BundleEndpoint.HttpsWeb.Acme.DomainName,
					"email":         federation.BundleEndpoint.HttpsWeb.Acme.Email,
				}
				if utils.StringToBool(federation.BundleEndpoint.HttpsWeb.Acme.TosAccepted) {
					acmeConfig["tos_accepted"] = true
				}
				webConfig["acme"] = acmeConfig
			}
			if federation.BundleEndpoint.HttpsWeb.ServingCert != nil {
				servingCertConfig := map[string]interface{}{}
				if federation.BundleEndpoint.HttpsWeb.ServingCert.FileSyncInterval > 0 {
					servingCertConfig["file_sync_interval"] = federation.BundleEndpoint.HttpsWeb.ServingCert.FileSyncInterval
				}
				webConfig["serving_cert_file"] = map[string]interface{}{
					"cert_file_path": "/run/spire/federation-tls/tls.crt",
					"key_file_path":  "/run/spire/federation-tls/tls.key",
				}
				if len(servingCertConfig) > 0 {
					for k, v := range servingCertConfig {
						webConfig[k] = v
					}
				}
			}
		}
		profileConfig["https_web"] = webConfig
	}
	bundleEndpoint["profile"] = profileConfig

	result := map[string]interface{}{
		"bundle_endpoint": bundleEndpoint,
	}

	// Configure federates_with
	if len(federation.FederatesWith) > 0 {
		federatesWith := map[string]interface{}{}
		for _, fw := range federation.FederatesWith {
			fwConfig := map[string]interface{}{
				"bundle_endpoint_url": fw.BundleEndpointUrl,
			}
			profileCfg := map[string]interface{}{}
			switch fw.BundleEndpointProfile {
			case v1alpha1.HttpsSpiffeProfile:
				profileCfg["https_spiffe"] = map[string]interface{}{
					"endpoint_spiffe_id": fw.EndpointSpiffeId,
				}
			case v1alpha1.HttpsWebProfile:
				profileCfg["https_web"] = map[string]interface{}{}
			}
			fwConfig["bundle_endpoint_profile"] = profileCfg
			federatesWith[fw.TrustDomain] = fwConfig
		}
		result["federates_with"] = federatesWith
	}

	return result
}
