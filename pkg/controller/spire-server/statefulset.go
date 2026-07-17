package spire_server

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/openshift/zero-trust-workload-identity-manager/api/v1alpha1"
	"github.com/openshift/zero-trust-workload-identity-manager/pkg/controller/status"
	"github.com/openshift/zero-trust-workload-identity-manager/pkg/controller/utils"
)

const (
	spireServerStatefulSetSpireServerConfigHashAnnotationKey            = "ztwim.openshift.io/spire-server-config-hash"
	spireServerStatefulSetSpireControllerManagerConfigHashAnnotationKey = "ztwim.openshift.io/spire-controller-manager-config-hash"
	spireServerStatefulSetUpstreamAgentConfigHashAnnotationKey          = "ztwim.openshift.io/upstream-agent-config-hash"
	spireServerHealthPort                                               = "server-healthz"
	spireCtrlMgrHealthPort                                              = "ctrlmgr-healthz"
	upstreamAgentHealthPort                                             = "agent-healthz"
)

// reconcileStatefulSet reconciles the Spire Server StatefulSet
func (r *SpireServerReconciler) reconcileStatefulSet(ctx context.Context, server *v1alpha1.SpireServer, statusMgr *status.Manager, createOnlyMode bool, spireServerConfigMapHash, spireControllerManagerConfigMapHash, upstreamAgentConfigHash string) error {
	sts := GenerateSpireServerStatefulSet(&server.Spec, spireServerConfigMapHash, spireControllerManagerConfigMapHash, upstreamAgentConfigHash)
	if err := controllerutil.SetControllerReference(server, sts, r.scheme); err != nil {
		r.log.Error(err, "failed to set controller reference on spire server stateful set resource")
		statusMgr.AddCondition(StatefulSetAvailable, "SpireServerStatefulSetGenerationFailed",
			err.Error(),
			metav1.ConditionFalse)
		return err
	}

	var existingSTS appsv1.StatefulSet
	err := r.ctrlClient.Get(ctx, types.NamespacedName{Name: sts.Name, Namespace: sts.Namespace}, &existingSTS)
	if err != nil && kerrors.IsNotFound(err) {
		if err = r.ctrlClient.Create(ctx, sts); err != nil {
			if conflictErr := utils.HandleCreateConflict(err, sts, r.log, statusMgr, StatefulSetAvailable); conflictErr != nil {
				return conflictErr
			}
			statusMgr.AddCondition(StatefulSetAvailable, "SpireServerStatefulSetCreationFailed",
				err.Error(),
				metav1.ConditionFalse)
			return fmt.Errorf("failed to create StatefulSet: %w", err)
		}
		r.log.Info("Created spire server StatefulSet")
	} else if err == nil {
		if needsUpdate(existingSTS, *sts) {
			if createOnlyMode {
				r.log.Info("Skipping StatefulSet update due to create-only mode")
			} else {
				sts.ResourceVersion = existingSTS.ResourceVersion
				if err = r.ctrlClient.Update(ctx, sts); err != nil {
					statusMgr.AddCondition(StatefulSetAvailable, "SpireServerStatefulSetUpdateFailed",
						err.Error(),
						metav1.ConditionFalse)
					return fmt.Errorf("failed to update StatefulSet: %w", err)
				}
				r.log.Info("Updated spire server StatefulSet")
			}
		}
	} else {
		r.log.Error(err, "failed to get spire server stateful set resource")
		statusMgr.AddCondition(StatefulSetAvailable, "SpireServerStatefulSetGetFailed",
			err.Error(),
			metav1.ConditionFalse)
		return err
	}

	// Check StatefulSet health/readiness
	statusMgr.CheckStatefulSetHealth(ctx, sts.Name, sts.Namespace, StatefulSetAvailable)

	return nil
}

const (
	// DBTLSMountPath is the fixed mount path for database TLS certificates
	DBTLSMountPath = "/run/spire/db/certs"
)

func GenerateSpireServerStatefulSet(config *v1alpha1.SpireServerSpec,
	spireServerConfigMapHash string,
	SpireControllerManagerConfigMapHash string,
	upstreamAgentConfigHash string) *appsv1.StatefulSet {

	// Generate standardized labels once and reuse them
	labels := utils.SpireServerLabels(config.Labels)

	// For selectors, we need only the core identifying labels (without custom user labels)
	selectorLabels := map[string]string{
		"app.kubernetes.io/name":      labels["app.kubernetes.io/name"],
		"app.kubernetes.io/instance":  labels["app.kubernetes.io/instance"],
		"app.kubernetes.io/component": labels["app.kubernetes.io/component"],
	}

	// Persistence is required, so we can directly access its fields.
	// Fields have defaults: Size="1Gi", AccessMode="ReadWriteOnce", StorageClass=""
	volumeResourceRequest := config.Persistence.Size
	volumeAccessMode := corev1.PersistentVolumeAccessMode(config.Persistence.AccessMode)

	var storageClassName *string
	if config.Persistence.StorageClass != "" {
		storageClassName = ptr.To(config.Persistence.StorageClass)
	}

	// Build base volume mounts for spire-server container
	spireServerVolumeMounts := []corev1.VolumeMount{
		{Name: "spire-server-socket", MountPath: "/tmp/spire-server/private"},
		{Name: "spire-config", MountPath: "/run/spire/config", ReadOnly: true},
		{Name: "spire-data", MountPath: "/run/spire/data"},
		{Name: "server-tmp", MountPath: "/tmp"},
	}

	// Build base volumes
	volumes := []corev1.Volume{
		{Name: "server-tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "spire-config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "spire-server"}}}},
		{Name: "spire-server-socket", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "spire-controller-manager-tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "controller-manager-config", VolumeSource: corev1.VolumeSource{ConfigMap: &corev1.ConfigMapVolumeSource{LocalObjectReference: corev1.LocalObjectReference{Name: "spire-controller-manager"}}}},
	}

	// Add database TLS Secret volume and mount if configured
	if config.Datastore.TLSSecretName != "" {
		// Add volume mount for the TLS secret at fixed path
		spireServerVolumeMounts = append(spireServerVolumeMounts, corev1.VolumeMount{
			Name:      "db-certs",
			MountPath: DBTLSMountPath,
		})

		// Add volume for the TLS secret
		volumes = append(volumes, corev1.Volume{
			Name: "db-certs",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: config.Datastore.TLSSecretName,
				},
			},
		})
	}
	podAnnotations := map[string]string{
		"kubectl.kubernetes.io/default-container":                           "spire-server",
		spireServerStatefulSetSpireServerConfigHashAnnotationKey:            spireServerConfigMapHash,
		spireServerStatefulSetSpireControllerManagerConfigHashAnnotationKey: SpireControllerManagerConfigMapHash,
	}
	if upstreamAgentConfigHash != "" {
		podAnnotations[spireServerStatefulSetUpstreamAgentConfigHashAnnotationKey] = upstreamAgentConfigHash
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "spire-server",
			Namespace: utils.GetOperatorNamespace(),
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas:    ptr.To(int32(1)),
			ServiceName: "spire-server",
			Selector: &metav1.LabelSelector{
				MatchLabels: selectorLabels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: podAnnotations,
					Labels:      labels,
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: "spire-server",
					Containers: []corev1.Container{
						{
							SecurityContext: &corev1.SecurityContext{
								ReadOnlyRootFilesystem: ptr.To(true),
							},
							Name:            "spire-server",
							Image:           utils.GetSpireServerImage(),
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            []string{"-expandEnv", "-config", "/run/spire/config/server.conf"},
							Env: []corev1.EnvVar{
								{Name: "PATH", Value: "/opt/spire/bin:/bin"},
							},
							Ports: []corev1.ContainerPort{
								{Name: "grpc", ContainerPort: 8081, Protocol: corev1.ProtocolTCP},
								{Name: spireServerHealthPort, ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/live", Port: intstr.FromString(spireServerHealthPort)}},
								InitialDelaySeconds: 15,
								PeriodSeconds:       60,
								TimeoutSeconds:      3,
								FailureThreshold:    2,
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/ready", Port: intstr.FromString(spireServerHealthPort)}},
								InitialDelaySeconds: 5,
								PeriodSeconds:       5,
							},
							Resources:    utils.DerefResourceRequirements(config.Resources),
							VolumeMounts: spireServerVolumeMounts,
						},
						{
							SecurityContext: &corev1.SecurityContext{
								ReadOnlyRootFilesystem: ptr.To(true),
							},
							Name:            "spire-controller-manager",
							Image:           utils.GetSpireControllerManagerImage(),
							ImagePullPolicy: corev1.PullIfNotPresent,
							Args:            []string{"--config=controller-manager-config.yaml"},
							Env: []corev1.EnvVar{
								{Name: "ENABLE_WEBHOOKS", Value: "true"},
							},
							Ports: []corev1.ContainerPort{
								{Name: "https", ContainerPort: 9443},
								{Name: spireCtrlMgrHealthPort, ContainerPort: 8083},
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/healthz", Port: intstr.FromString(spireCtrlMgrHealthPort)}},
							},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/readyz", Port: intstr.FromString(spireCtrlMgrHealthPort)}},
							},
							VolumeMounts: []corev1.VolumeMount{
								{Name: "spire-server-socket", MountPath: "/tmp/spire-server/private", ReadOnly: true},
								{Name: "controller-manager-config", MountPath: "/controller-manager-config.yaml", SubPath: "controller-manager-config.yaml", ReadOnly: true},
								{Name: "spire-controller-manager-tmp", MountPath: "/tmp", SubPath: "spire-controller-manager"},
							},
							Resources: utils.DerefResourceRequirements(config.Resources),
						},
					},
					Volumes:      volumes,
					Affinity:     config.Affinity,
					NodeSelector: utils.DerefNodeSelector(config.NodeSelector),
					Tolerations:  utils.DerefTolerations(config.Tolerations),
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "spire-data"},
					Spec: corev1.PersistentVolumeClaimSpec{
						AccessModes:      []corev1.PersistentVolumeAccessMode{volumeAccessMode},
						StorageClassName: storageClassName,
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse(volumeResourceRequest),
							},
						},
					},
				},
			},
		},
	}

	// Add proxy configuration if enabled
	utils.AddProxyConfigToPod(&sts.Spec.Template.Spec)

	// Add federation configuration if present
	if config.Federation != nil {
		addFederationConfigurationToStatefulSet(sts, config.Federation)
	}

	if config.UpstreamAuthority != nil {
		addUpstreamAuthorityToStatefulSet(sts, config.UpstreamAuthority)
	}

	if config.AdditionalNodeAttestors != nil && config.AdditionalNodeAttestors.X509pop != nil {
		addServerX509popAttestorToStatefulSet(sts, config.AdditionalNodeAttestors.X509pop)
	}

	return sts
}

func addUpstreamAuthorityToStatefulSet(sts *appsv1.StatefulSet, ua *v1alpha1.UpstreamAuthorityConfig) {
	if ua.Vault != nil {
		addVaultUpstreamAuthorityToStatefulSet(sts, ua.Vault)
	}
	if ua.Spire != nil {
		addSpireUpstreamAuthorityToStatefulSet(sts, ua.Spire)
	}
}

func addVaultUpstreamAuthorityToStatefulSet(sts *appsv1.StatefulSet, v *v1alpha1.UpstreamAuthorityVault) {
	if v.K8sAuth != nil {
		audience := v.K8sAuth.Audience
		if audience == "" {
			audience = vaultTokenFileName
		}
		expirationSeconds := int64(600)

		sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "vault-token",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources: []corev1.VolumeProjection{
							{
								ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
									Audience:          audience,
									ExpirationSeconds: &expirationSeconds,
									Path:              vaultTokenFileName,
								},
							},
						},
					},
				},
			},
		)

		sts.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			sts.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{
				Name:      "vault-token",
				MountPath: vaultTokenMountDir,
				ReadOnly:  true,
			},
		)
	}

	if v.CACertSecretRef != nil {
		sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "upstream-ca",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: v.CACertSecretRef.Name,
						Items: []corev1.KeyToPath{
							{
								Key:  v.CACertSecretRef.Key,
								Path: upstreamCACertFileName,
							},
						},
					},
				},
			},
		)

		sts.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			sts.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{
				Name:      "upstream-ca",
				MountPath: upstreamCAMountPath,
				ReadOnly:  true,
			},
		)
	}
}

// addSpireUpstreamAuthorityToStatefulSet injects the upstream-agent sidecar
// container and its volumes into the SPIRE server StatefulSet for nested SPIRE.
func addSpireUpstreamAuthorityToStatefulSet(sts *appsv1.StatefulSet, spire *v1alpha1.UpstreamAuthoritySpire) {
	// Enable shared PID namespace so the upstream-agent's unix WorkloadAttestor
	// can resolve the spire-server's process via /proc when it connects to the
	// Workload API socket. Without this, SO_PEERCRED returns a PID that the
	// attestor can't look up because containers have separate PID namespaces.
	sts.Spec.Template.Spec.ShareProcessNamespace = ptr.To(true)

	// --- Volumes ---

	// emptyDir for Workload API socket sharing between sidecar and spire-server
	sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes,
		corev1.Volume{
			Name:         "upstream-agent-socket",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
	)

	// emptyDir for upstream-agent key/data persistence
	sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes,
		corev1.Volume{
			Name:         "upstream-agent-data",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		},
	)

	// ConfigMap for upstream-agent agent.conf
	sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes,
		corev1.Volume{
			Name: "upstream-agent-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: "upstream-agent-config"},
				},
			},
		},
	)

	// Attestation-specific volumes
	if spire.NodeAttestor.X509pop != nil {
		sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "x509pop-cert",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: spire.NodeAttestor.X509pop.CertificateSecretName,
					},
				},
			},
		)
	}

	if spire.NodeAttestor.K8sPsat != nil {
		expirationSeconds := int64(7200)
		sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "upstream-agent-token",
				VolumeSource: corev1.VolumeSource{
					Projected: &corev1.ProjectedVolumeSource{
						Sources: []corev1.VolumeProjection{
							{
								ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
									Audience:          "spire-server",
									ExpirationSeconds: &expirationSeconds,
									Path:              "spire-agent",
								},
							},
						},
					},
				},
			},
		)
	}

	// Secret for upstream trust bundle (if not using insecure_bootstrap)
	if spire.TrustBundle.SecretRef != nil {
		sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "upstream-bundle",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: spire.TrustBundle.SecretRef.Name,
						Items: []corev1.KeyToPath{
							{
								Key:  spire.TrustBundle.SecretRef.Key,
								Path: upstreamBundleFileName,
							},
						},
					},
				},
			},
		)
	}

	// --- spire-server container: mount the socket dir (read-only) ---
	sts.Spec.Template.Spec.Containers[0].VolumeMounts = append(
		sts.Spec.Template.Spec.Containers[0].VolumeMounts,
		corev1.VolumeMount{
			Name:      "upstream-agent-socket",
			MountPath: upstreamAgentSocketDir,
			ReadOnly:  true,
		},
	)

	// --- upstream-agent sidecar container ---
	sidecarVolumeMounts := []corev1.VolumeMount{
		{Name: "upstream-agent-socket", MountPath: upstreamAgentSocketDir},
		{Name: "upstream-agent-data", MountPath: upstreamAgentDataDir},
		{Name: "upstream-agent-config", MountPath: upstreamAgentConfigDir, ReadOnly: true},
	}

	if spire.NodeAttestor.X509pop != nil {
		sidecarVolumeMounts = append(sidecarVolumeMounts,
			corev1.VolumeMount{Name: "x509pop-cert", MountPath: x509popCertMountPath, ReadOnly: true},
		)
	}

	if spire.NodeAttestor.K8sPsat != nil {
		sidecarVolumeMounts = append(sidecarVolumeMounts,
			corev1.VolumeMount{Name: "upstream-agent-token", MountPath: upstreamAgentTokenMountPath, ReadOnly: true},
		)
	}

	if spire.TrustBundle.SecretRef != nil {
		sidecarVolumeMounts = append(sidecarVolumeMounts,
			corev1.VolumeMount{
				Name:      "upstream-bundle",
				MountPath: upstreamBundleMountPath,
				ReadOnly:  true,
			},
		)
	}

	sidecar := corev1.Container{
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{
					"ALL",
				},
			},
			ReadOnlyRootFilesystem: ptr.To(true),
		},
		Name:            "upstream-agent",
		Image:           utils.GetSpireAgentImage(),
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            []string{"-expandEnv", "-config", upstreamAgentConfigDir + "/" + upstreamAgentConfigFile},
		Env: []corev1.EnvVar{
			{Name: "PATH", Value: "/opt/spire/bin:/bin"},
		},
		Ports: []corev1.ContainerPort{
			{Name: upstreamAgentHealthPort, ContainerPort: 9983, Protocol: corev1.ProtocolTCP},
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/live", Port: intstr.FromString(upstreamAgentHealthPort)}},
			InitialDelaySeconds: 15,
			PeriodSeconds:       60,
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler:        corev1.ProbeHandler{HTTPGet: &corev1.HTTPGetAction{Path: "/ready", Port: intstr.FromString(upstreamAgentHealthPort)}},
			InitialDelaySeconds: 10,
			PeriodSeconds:       30,
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("50m"),
				corev1.ResourceMemory: resource.MustParse("64Mi"),
			},
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		VolumeMounts: sidecarVolumeMounts,
	}

	sts.Spec.Template.Spec.Containers = append(sts.Spec.Template.Spec.Containers, sidecar)
}

// addServerX509popAttestorToStatefulSet mounts the x509pop CA bundle into the
// SPIRE server container so it can verify downstream agent certificates.
func addServerX509popAttestorToStatefulSet(sts *appsv1.StatefulSet, x509pop *v1alpha1.ServerX509popAttestorConfig) {
	sts.Spec.Template.Spec.Volumes = append(sts.Spec.Template.Spec.Volumes,
		corev1.Volume{
			Name: "x509pop-server-ca",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: x509pop.CABundleSecretRef.Name,
					Items: []corev1.KeyToPath{
						{
							Key:  x509pop.CABundleSecretRef.Key,
							Path: x509popServerCAFileName,
						},
					},
				},
			},
		},
	)

	sts.Spec.Template.Spec.Containers[0].VolumeMounts = append(
		sts.Spec.Template.Spec.Containers[0].VolumeMounts,
		corev1.VolumeMount{
			Name:      "x509pop-server-ca",
			MountPath: x509popServerCAMountPath,
			ReadOnly:  true,
		},
	)
}

// addFederationConfigurationToStatefulSet adds federation port, volume and mount to the StatefulSet
func addFederationConfigurationToStatefulSet(sts *appsv1.StatefulSet, federation *v1alpha1.FederationConfig) {
	// Add federation port to spire-server container (first container)
	sts.Spec.Template.Spec.Containers[0].Ports = append(
		sts.Spec.Template.Spec.Containers[0].Ports,
		corev1.ContainerPort{Name: "federation", ContainerPort: 8443, Protocol: corev1.ProtocolTCP},
	)

	// Only add spire-server-tls volume if ServingCert is configured
	if federation.BundleEndpoint.HttpsWeb != nil && federation.BundleEndpoint.HttpsWeb.ServingCert != nil {
		// Always use service CA certificate for internal communication
		secretName := utils.SpireServerServingCertName

		// Add volume mount to spire-server container (first container)
		sts.Spec.Template.Spec.Containers[0].VolumeMounts = append(
			sts.Spec.Template.Spec.Containers[0].VolumeMounts,
			corev1.VolumeMount{
				Name:      "spire-server-tls",
				MountPath: "/run/spire/server-tls",
				ReadOnly:  true,
			},
		)

		// Add volume to pod spec with unified name
		sts.Spec.Template.Spec.Volumes = append(
			sts.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "spire-server-tls",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: secretName,
					},
				},
			},
		)
	}
}
