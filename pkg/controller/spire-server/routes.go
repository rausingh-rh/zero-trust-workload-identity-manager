package spire_server

import (
	"context"
	"fmt"

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

// reconcileFederationRoute reconciles the federation endpoint Route for the SPIRE Server.
// When managedRoute is "true", it creates or updates a Route to expose the federation bundle endpoint externally.
// When managedRoute is "false", it skips Route management and sets the condition accordingly.
func (r *SpireServerReconciler) reconcileFederationRoute(ctx context.Context, server *v1alpha1.SpireServer, statusMgr *status.Manager, createOnlyMode bool) error {
	if server.Spec.Federation == nil {
		return nil
	}

	if utils.StringToBool(server.Spec.Federation.ManagedRoute) {
		route := generateFederationRoute(server)

		var existingRoute routev1.Route
		err := r.ctrlClient.Get(ctx, types.NamespacedName{
			Name:      route.Name,
			Namespace: route.Namespace,
		}, &existingRoute)
		if err != nil {
			if kerrors.IsNotFound(err) {
				if err = r.ctrlClient.Create(ctx, route); err != nil {
					r.log.Error(err, "Failed to create federation route")
					statusMgr.AddCondition(FederationRouteAvailable, "FederationRouteCreationFailed",
						err.Error(),
						metav1.ConditionFalse)
					return err
				}

				statusMgr.AddCondition(FederationRouteAvailable, "FederationRouteCreated",
					"SPIRE federation managed Route created",
					metav1.ConditionTrue)

				r.log.Info("Created federation route", "Namespace", route.Namespace, "Name", route.Name)
			} else {
				r.log.Error(err, "Failed to get existing federation route")
				statusMgr.AddCondition(FederationRouteAvailable, "FederationRouteRetrievalFailed",
					err.Error(),
					metav1.ConditionFalse)
				return err
			}
		} else if checkFederationRouteConflict(&existingRoute, route) {
			r.log.Info("Found conflict in federation route, updating")
			route.ResourceVersion = existingRoute.ResourceVersion

			if createOnlyMode {
				r.log.Info("Skipping federation Route update due to create-only mode", "Namespace", route.Namespace, "Name", route.Name)
			} else {
				err = r.ctrlClient.Update(ctx, route)
				if err != nil {
					statusMgr.AddCondition(FederationRouteAvailable, "FederationRouteUpdateFailed",
						err.Error(),
						metav1.ConditionFalse)
					return err
				}

				statusMgr.AddCondition(FederationRouteAvailable, "FederationRouteUpdated",
					"SPIRE federation managed Route updated",
					metav1.ConditionTrue)

				r.log.Info("Updated federation route", "Namespace", route.Namespace, "Name", route.Name)
			}
		} else {
			// Route exists and is up to date — only update status if it's currently not ready
			existingCondition := apimeta.FindStatusCondition(server.Status.ConditionalStatus.Conditions, FederationRouteAvailable)
			if existingCondition == nil || existingCondition.Status != metav1.ConditionTrue {
				statusMgr.AddCondition(FederationRouteAvailable, "FederationRouteReady",
					"SPIRE federation managed Route is ready",
					metav1.ConditionTrue)
			}
		}
	} else {
		statusMgr.AddCondition(FederationRouteAvailable, "FederationRouteDisabled",
			"SPIRE federation managed Route disabled",
			metav1.ConditionFalse)
	}

	return nil
}

// checkFederationRouteConflict returns true if desired and current routes have conflicts
func checkFederationRouteConflict(current, desired *routev1.Route) bool {
	return !equality.Semantic.DeepEqual(current.Spec, desired.Spec) || !equality.Semantic.DeepEqual(current.Labels, desired.Labels)
}

// generateFederationRoute creates an OpenShift Route resource for the SPIRE Server federation bundle endpoint
func generateFederationRoute(server *v1alpha1.SpireServer) *routev1.Route {
	labels := utils.SpireServerLabels(server.Spec.Labels)

	route := &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spire-server-federation",
			Namespace: utils.GetOperatorNamespace(),
			Labels:    labels,
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
				Kind:   "Service",
				Name:   "spire-server-federation",
				Weight: &[]int32{100}[0],
			},
			WildcardPolicy: routev1.WildcardPolicyNone,
		},
	}

	// If using https_web profile with serving cert, attach the external certificate to the Route
	if server.Spec.Federation.BundleEndpoint.Profile == v1alpha1.HttpsWebProfile &&
		server.Spec.Federation.BundleEndpoint.HttpsWeb != nil &&
		server.Spec.Federation.BundleEndpoint.HttpsWeb.ServingCert != nil &&
		server.Spec.Federation.BundleEndpoint.HttpsWeb.ServingCert.ExternalSecretRef != "" {
		route.Spec.TLS.Termination = routev1.TLSTerminationReencrypt
		route.Spec.TLS.InsecureEdgeTerminationPolicy = routev1.InsecureEdgeTerminationPolicyRedirect
		route.Spec.TLS.ExternalCertificate = &routev1.LocalObjectReference{
			Name: server.Spec.Federation.BundleEndpoint.HttpsWeb.ServingCert.ExternalSecretRef,
		}
	}

	return route
}

// deleteFederationRoute removes the federation Route if it exists. Called when federation
// configuration is removed or managedRoute is disabled.
func (r *SpireServerReconciler) deleteFederationRoute(ctx context.Context) error {
	route := &routev1.Route{}
	err := r.ctrlClient.Get(ctx, types.NamespacedName{
		Name:      "spire-server-federation",
		Namespace: utils.GetOperatorNamespace(),
	}, route)
	if err != nil {
		if kerrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("failed to get federation route for deletion: %w", err)
	}

	if err := r.ctrlClient.Delete(ctx, route); err != nil && !kerrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete federation route: %w", err)
	}
	r.log.Info("Deleted federation route")
	return nil
}
