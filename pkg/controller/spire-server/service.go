package spire_server

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/openshift/zero-trust-workload-identity-manager/api/v1alpha1"
	"github.com/openshift/zero-trust-workload-identity-manager/pkg/controller/status"
	"github.com/openshift/zero-trust-workload-identity-manager/pkg/controller/utils"
	"github.com/openshift/zero-trust-workload-identity-manager/pkg/operator/assets"
)

// reconcileService reconciles all Services (spire-server, controller-manager, and federation)
func (r *SpireServerReconciler) reconcileService(ctx context.Context, server *v1alpha1.SpireServer, statusMgr *status.Manager, createOnlyMode bool) error {
	// Spire Server Service
	if err := r.reconcileSpireServerService(ctx, server, statusMgr, createOnlyMode); err != nil {
		return err
	}

	// Controller Manager Webhook Service
	if err := r.reconcileSpireControllerManagerService(ctx, server, statusMgr, createOnlyMode); err != nil {
		return err
	}

	// Federation Service (only when federation is configured)
	if server.Spec.Federation != nil {
		if err := r.reconcileFederationService(ctx, server, statusMgr, createOnlyMode); err != nil {
			return err
		}
	}

	statusMgr.AddCondition(ServiceAvailable, v1alpha1.ReasonReady,
		"All Service resources available",
		metav1.ConditionTrue)

	return nil
}

// reconcileSpireServerService reconciles the Spire Server Service
func (r *SpireServerReconciler) reconcileSpireServerService(ctx context.Context, server *v1alpha1.SpireServer, statusMgr *status.Manager, createOnlyMode bool) error {
	desired := getSpireServerService(server.Spec.Labels)

	if err := controllerutil.SetControllerReference(server, desired, r.scheme); err != nil {
		r.log.Error(err, "failed to set controller reference on service")
		statusMgr.AddCondition(ServiceAvailable, v1alpha1.ReasonFailed,
			fmt.Sprintf("Failed to set owner reference on Service: %v", err),
			metav1.ConditionFalse)
		return err
	}

	// Get existing resource (from cache)
	existing := &corev1.Service{}
	err := r.ctrlClient.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)

	if err != nil {
		if !kerrors.IsNotFound(err) {
			// Unexpected error
			r.log.Error(err, "failed to get service")
			statusMgr.AddCondition(ServiceAvailable, v1alpha1.ReasonFailed,
				fmt.Sprintf("Failed to get Service: %v", err),
				metav1.ConditionFalse)
			return err
		}

		// Resource doesn't exist, create it
		if err := r.ctrlClient.Create(ctx, desired); err != nil {
			r.log.Error(err, "failed to create service")
			statusMgr.AddCondition(ServiceAvailable, v1alpha1.ReasonFailed,
				fmt.Sprintf("Failed to create Service: %v", err),
				metav1.ConditionFalse)
			return err
		}

		r.log.Info("Created Service", "name", desired.Name, "namespace", desired.Namespace)
		return nil
	}

	// Resource exists, check if we need to update
	if createOnlyMode {
		r.log.V(1).Info("Service exists, skipping update due to create-only mode", "name", desired.Name)
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

	// Normalize ports - set default protocol to TCP if not specified
	for i := range desired.Spec.Ports {
		if desired.Spec.Ports[i].Protocol == "" {
			desired.Spec.Ports[i].Protocol = corev1.ProtocolTCP
		}
	}

	// Check if update is needed
	if !utils.ResourceNeedsUpdate(existing, desired) {
		r.log.V(1).Info("Service is up to date", "name", desired.Name)
		return nil
	}

	// Update the resource
	if err := r.ctrlClient.Update(ctx, desired); err != nil {
		r.log.Error(err, "failed to update service")
		statusMgr.AddCondition(ServiceAvailable, v1alpha1.ReasonFailed,
			fmt.Sprintf("Failed to update Service: %v", err),
			metav1.ConditionFalse)
		return err
	}

	r.log.Info("Updated Service", "name", desired.Name, "namespace", desired.Namespace)
	return nil
}

// reconcileSpireControllerManagerService reconciles the Controller Manager webhook Service
func (r *SpireServerReconciler) reconcileSpireControllerManagerService(ctx context.Context, server *v1alpha1.SpireServer, statusMgr *status.Manager, createOnlyMode bool) error {
	desired := getSpireControllerManagerWebhookService(server.Spec.Labels)

	if err := controllerutil.SetControllerReference(server, desired, r.scheme); err != nil {
		r.log.Error(err, "failed to set controller reference on controller manager service")
		statusMgr.AddCondition(ServiceAvailable, v1alpha1.ReasonFailed,
			fmt.Sprintf("Failed to set owner reference on Controller Manager Service: %v", err),
			metav1.ConditionFalse)
		return err
	}

	// Get existing resource (from cache)
	existing := &corev1.Service{}
	err := r.ctrlClient.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)

	if err != nil {
		if !kerrors.IsNotFound(err) {
			// Unexpected error
			r.log.Error(err, "failed to get controller manager service")
			statusMgr.AddCondition(ServiceAvailable, v1alpha1.ReasonFailed,
				fmt.Sprintf("Failed to get Controller Manager Service: %v", err),
				metav1.ConditionFalse)
			return err
		}

		// Resource doesn't exist, create it
		if err := r.ctrlClient.Create(ctx, desired); err != nil {
			r.log.Error(err, "failed to create controller manager service")
			statusMgr.AddCondition(ServiceAvailable, v1alpha1.ReasonFailed,
				fmt.Sprintf("Failed to create Controller Manager Service: %v", err),
				metav1.ConditionFalse)
			return err
		}

		r.log.Info("Created Service", "name", desired.Name, "namespace", desired.Namespace)
		return nil
	}

	// Resource exists, check if we need to update
	if createOnlyMode {
		r.log.V(1).Info("Service exists, skipping update due to create-only mode", "name", desired.Name)
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

	// Normalize ports - set default protocol to TCP if not specified
	for i := range desired.Spec.Ports {
		if desired.Spec.Ports[i].Protocol == "" {
			desired.Spec.Ports[i].Protocol = corev1.ProtocolTCP
		}
	}

	// Check if update is needed
	if !utils.ResourceNeedsUpdate(existing, desired) {
		r.log.V(1).Info("Service is up to date", "name", desired.Name)
		return nil
	}

	// Update the resource
	if err := r.ctrlClient.Update(ctx, desired); err != nil {
		r.log.Error(err, "failed to update controller manager service")
		statusMgr.AddCondition(ServiceAvailable, v1alpha1.ReasonFailed,
			fmt.Sprintf("Failed to update Controller Manager Service: %v", err),
			metav1.ConditionFalse)
		return err
	}

	r.log.Info("Updated Service", "name", desired.Name, "namespace", desired.Namespace)
	return nil
}

// getSpireServerService returns the Spire Server Service with proper labels and selectors
func getSpireServerService(customLabels map[string]string) *corev1.Service {
	svc := utils.DecodeServiceObjBytes(assets.MustAsset(utils.SpireServerServiceAssetName))
	svc.Labels = utils.SpireServerLabels(customLabels)
	svc.Namespace = utils.GetOperatorNamespace()
	svc.Spec.Selector = map[string]string{
		"app.kubernetes.io/name":     "spire-server",
		"app.kubernetes.io/instance": utils.StandardInstance,
	}
	return svc
}

// getSpireControllerManagerWebhookService returns the Controller Manager Service with proper labels and selectors
func getSpireControllerManagerWebhookService(customLabels map[string]string) *corev1.Service {
	svc := utils.DecodeServiceObjBytes(assets.MustAsset(utils.SpireControllerMangerWebhookServiceAssetName))
	svc.Labels = utils.SpireControllerManagerLabels(customLabels)
	svc.Namespace = utils.GetOperatorNamespace()
	svc.Spec.Selector = map[string]string{
		"app.kubernetes.io/name":     "spire-controller-manager",
		"app.kubernetes.io/instance": utils.StandardInstance,
	}
	return svc
}

// reconcileFederationService reconciles the federation bundle endpoint Service
func (r *SpireServerReconciler) reconcileFederationService(ctx context.Context, server *v1alpha1.SpireServer, statusMgr *status.Manager, createOnlyMode bool) error {
	desired := getFederationService(server.Spec.Labels)

	if err := controllerutil.SetControllerReference(server, desired, r.scheme); err != nil {
		r.log.Error(err, "failed to set controller reference on federation service")
		statusMgr.AddCondition(FederationServiceAvailable, v1alpha1.ReasonFailed,
			fmt.Sprintf("Failed to set owner reference on Federation Service: %v", err),
			metav1.ConditionFalse)
		return err
	}

	existing := &corev1.Service{}
	err := r.ctrlClient.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)

	if err != nil {
		if !kerrors.IsNotFound(err) {
			r.log.Error(err, "failed to get federation service")
			statusMgr.AddCondition(FederationServiceAvailable, v1alpha1.ReasonFailed,
				fmt.Sprintf("Failed to get Federation Service: %v", err),
				metav1.ConditionFalse)
			return err
		}

		if err := r.ctrlClient.Create(ctx, desired); err != nil {
			r.log.Error(err, "failed to create federation service")
			statusMgr.AddCondition(FederationServiceAvailable, v1alpha1.ReasonFailed,
				fmt.Sprintf("Failed to create Federation Service: %v", err),
				metav1.ConditionFalse)
			return err
		}

		r.log.Info("Created Federation Service", "name", desired.Name, "namespace", desired.Namespace)
		statusMgr.AddCondition(FederationServiceAvailable, v1alpha1.ReasonReady,
			"Federation Service created",
			metav1.ConditionTrue)
		return nil
	}

	if createOnlyMode {
		r.log.V(1).Info("Federation Service exists, skipping update due to create-only mode", "name", desired.Name)
		return nil
	}

	// Preserve Kubernetes-managed fields
	desired.ResourceVersion = existing.ResourceVersion
	desired.Spec.ClusterIP = existing.Spec.ClusterIP
	desired.Spec.ClusterIPs = existing.Spec.ClusterIPs
	desired.Spec.IPFamilies = existing.Spec.IPFamilies
	desired.Spec.IPFamilyPolicy = existing.Spec.IPFamilyPolicy
	desired.Spec.InternalTrafficPolicy = existing.Spec.InternalTrafficPolicy
	desired.Spec.SessionAffinity = existing.Spec.SessionAffinity

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
			fmt.Sprintf("Failed to update Federation Service: %v", err),
			metav1.ConditionFalse)
		return err
	}

	r.log.Info("Updated Federation Service", "name", desired.Name, "namespace", desired.Namespace)
	statusMgr.AddCondition(FederationServiceAvailable, v1alpha1.ReasonReady,
		"Federation Service updated",
		metav1.ConditionTrue)
	return nil
}

// getFederationService returns the federation bundle endpoint Service
func getFederationService(customLabels map[string]string) *corev1.Service {
	labels := utils.SpireServerLabels(customLabels)
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spire-server-federation",
			Namespace: utils.GetOperatorNamespace(),
			Labels:    labels,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Name:       "federation",
					Port:       8443,
					TargetPort: intstr.FromInt32(8443),
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
