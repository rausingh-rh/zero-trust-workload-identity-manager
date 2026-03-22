package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="SpireServer is a singleton, .metadata.name must be 'cluster'"
// +kubebuilder:validation:XValidation:rule="oldSelf.spec.persistence.size == self.spec.persistence.size",message="spec.persistence.size is immutable"
// +kubebuilder:validation:XValidation:rule="oldSelf.spec.persistence.accessMode == self.spec.persistence.accessMode",message="spec.persistence.accessMode is immutable"
// +kubebuilder:validation:XValidation:rule="oldSelf.spec.persistence.storageClass == self.spec.persistence.storageClass",message="spec.persistence.storageClass is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.spec.federation) || has(self.spec.federation)",message="spec.federation cannot be removed once configured"
// +operator-sdk:csv:customresourcedefinitions:displayName="SpireServer"

// SpireServer defines the configuration for the SPIRE Server managed by zero trust workload identity manager.
// This includes details related to trust domain, data storage, plugins
// and other configs required for workload authentication.
type SpireServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              SpireServerSpec   `json:"spec,omitempty"`
	Status            SpireServerStatus `json:"status,omitempty"`
}

// SpireServerSpec will have specifications for configuration related to the spire server.
type SpireServerSpec struct {
	// logLevel sets the logging level for the operand.
	// Valid values are: debug, info, warn, error.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=debug;info;warn;error
	// +kubebuilder:default:="info"
	LogLevel string `json:"logLevel,omitempty"`

	// logFormat sets the logging format for the operand.
	// Valid values are: text, json.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=text;json
	// +kubebuilder:default:="text"
	LogFormat string `json:"logFormat,omitempty"`

	// jwtIssuer is the JWT issuer url.
	// Must be a valid HTTPS or HTTP URL.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=512
	// +kubebuilder:validation:Pattern=`^(?i)https?://[^\s?#]+$`
	JwtIssuer string `json:"jwtIssuer"`

	// caValidity is the validity period (TTL) for the SPIRE Server's own CA certificate.
	// This determines how long the server's root or intermediate certificate is valid.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="24h"
	CAValidity metav1.Duration `json:"caValidity"`

	// defaultX509Validity is the default validity period (TTL) for X.509 SVIDs issued to workloads.
	// This value is used if a specific TTL is not configured for a registration entry.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="1h"
	DefaultX509Validity metav1.Duration `json:"defaultX509Validity"`

	// defaultJWTValidity is the default validity period (TTL) for JWT SVIDs issued to workloads.
	// This value is used if a specific TTL is not configured for a registration entry.
	// +kubebuilder:validation:Type=string
	// +kubebuilder:validation:Format=duration
	// +kubebuilder:validation:Optional
	// +kubebuilder:default="5m"
	DefaultJWTValidity metav1.Duration `json:"defaultJWTValidity"`

	// caKeyType specifies the key type used for the server CA (both X509 and JWT).
	// Valid values are: rsa-2048, rsa-4096, ec-p256, ec-p384.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=rsa-2048;rsa-4096;ec-p256;ec-p384
	// +kubebuilder:default="rsa-2048"
	CAKeyType string `json:"caKeyType,omitempty"`

	// jwtKeyType specifies the key type used for JWT signing.
	// Valid values are: rsa-2048, rsa-4096, ec-p256, ec-p384.
	// This field is optional and will only be set in the SPIRE server configuration if explicitly provided.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=rsa-2048;rsa-4096;ec-p256;ec-p384
	JWTKeyType string `json:"jwtKeyType,omitempty"`

	// keyManager has configs for the spire server key manager.
	// +kubebuilder:validation:Optional
	KeyManager *KeyManager `json:"keyManager,omitempty"`

	// caSubject contains subject information for the Spire CA.
	// +kubebuilder:validation:Required
	CASubject CASubject `json:"caSubject,omitempty"`

	// persistence has config for spire server volume related configs.
	// This field is required and immutable once set.
	// +kubebuilder:validation:Required
	Persistence Persistence `json:"persistence"`

	// spireSQLConfig has the config required for the spire server SQL DataStore.
	// +kubebuilder:validation:Required
	Datastore DataStore `json:"datastore,omitempty"`

	// federation configures SPIRE federation endpoints and relationships for cross-cluster
	// workload communication. When set, the operator creates the necessary Service, Route,
	// and ConfigMap entries to expose a federation bundle endpoint and configure trust
	// relationships with remote clusters.
	// Once configured, this field cannot be removed; to disable federation the system
	// must be uninstalled and reinstalled. Peer configurations (federatesWith) remain
	// dynamic and can be added or removed at any time.
	// +kubebuilder:validation:Optional
	Federation *FederationConfig `json:"federation,omitempty"`

	CommonConfig `json:",inline"`
}

// Persistence defines volume-related settings.
type Persistence struct {
	// size of the persistent volume (e.g., 1Gi).
	// +kubebuilder:validation:Pattern=^[1-9][0-9]*Gi$
	// +kubebuilder:default:="1Gi"
	Size string `json:"size"`

	// accessMode for the volume.
	// +kubebuilder:validation:Enum=ReadWriteOnce;ReadWriteOncePod;ReadWriteMany
	// +kubebuilder:default:=ReadWriteOnce
	AccessMode string `json:"accessMode"`

	// storageClass to be used for the PVC.
	// +kubebuilder:validation:optional
	// +kubebuilder:default:=""
	StorageClass string `json:"storageClass,omitempty"`
}

// DataStore configures the Spire SQL datastore backend.
type DataStore struct {
	// databaseType specifies type of database to use.
	// +kubebuilder:validation:Enum=sql;sqlite3;postgres;mysql;aws_postgresql;aws_mysql
	// +kubebuilder:default:=sqlite3
	DatabaseType string `json:"databaseType"`

	// connectionString contain connection credentials required for spire server Datastore.
	// Must not be empty and should contain valid connection parameters for the specified database type.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:default:=/run/spire/data/datastore.sqlite3
	ConnectionString string `json:"connectionString"`

	// DB pool config
	// maxOpenConns will specify the maximum connections for the DB pool.
	// Must be between 1 and 10000.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10000
	// +kubebuilder:default:=100
	MaxOpenConns int `json:"maxOpenConns"`

	// maxIdleConns specifies the maximum idle connection to be configured.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10000
	// +kubebuilder:default:=2
	MaxIdleConns int `json:"maxIdleConns"`

	// connMaxLifetime will specify maximum lifetime connections.
	// Max time (in seconds) a connection may live.
	// +kubebuilder:validation:Minimum=0
	ConnMaxLifetime int `json:"connMaxLifetime"`

	// disableMigration specifies the migration state
	// If true, disables DB auto-migration.
	// +kubebuilder:default:="false"
	// +kubebuilder:validation:Enum:="true";"false"
	// +kubebuilder:validation:Optional
	DisableMigration string `json:"disableMigration"`
}

// KeyManager will contain configs for the spire server key manager
type KeyManager struct {
	// diskEnabled is a flag to enable keyManager on disk.
	// +kubebuilder:default:="true"
	// +kubebuilder:validation:Enum:="true";"false"
	// +kubebuilder:validation:Optional
	DiskEnabled string `json:"diskEnabled,omitempty"`

	// memoryEnabled is a flag to enable keyManager on memory
	// +kubebuilder:default:="false"
	// +kubebuilder:validation:Enum:="true";"false"
	// +kubebuilder:validation:Optional
	MemoryEnabled string `json:"memoryEnabled,omitempty"`
}

// CASubject defines the subject information for the Spire CA.
type CASubject struct {
	// country specifies the country for the CA.
	// ISO 3166-1 alpha-2 country code (2 characters).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=2
	Country string `json:"country,omitempty"`

	// organization specifies the organization for the CA.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=64
	Organization string `json:"organization,omitempty"`

	// commonName specifies the common name for the CA.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=255
	CommonName string `json:"commonName,omitempty"`
}

// FederationConfig defines federation bundle endpoint and federated trust domain configuration
// for enabling cross-cluster workload communication via SPIRE federation.
type FederationConfig struct {
	// bundleEndpoint configures this cluster's federation bundle endpoint, which exposes
	// the SPIRE trust bundle over HTTPS on port 8443 so that remote clusters can retrieve
	// it for federation trust establishment.
	// +kubebuilder:validation:Required
	BundleEndpoint BundleEndpointConfig `json:"bundleEndpoint"`

	// federatesWith lists the remote trust domains that this cluster federates with.
	// Each entry specifies a remote trust domain and its bundle endpoint URL so that
	// the local SPIRE Server can retrieve the remote trust bundle for cross-cluster
	// workload authentication. A maximum of 50 federated trust domains is supported.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	// +listType=map
	// +listMapKey=trustDomain
	FederatesWith []FederatesWithConfig `json:"federatesWith,omitempty"`

	// managedRoute controls whether the operator automatically creates and manages an
	// OpenShift Route to expose the federation bundle endpoint externally.
	// When set to "true", the operator creates a Route for external access to the
	// federation endpoint. When set to "false", the operator does not create or manage
	// a Route, allowing administrators to manually configure custom Routes or ingress
	// solutions for more granular control.
	// +kubebuilder:default:="true"
	// +kubebuilder:validation:Enum:="true";"false"
	// +kubebuilder:validation:Optional
	ManagedRoute string `json:"managedRoute,omitempty"`
}

// BundleEndpointConfig configures how this cluster exposes its federation trust bundle.
// The federation endpoint is exposed on 0.0.0.0:8443 and serves the SPIRE trust bundle
// for retrieval by remote federated clusters.
// +kubebuilder:validation:XValidation:rule="self.profile == 'https_web' ? has(self.httpsWeb) : true",message="httpsWeb is required when profile is https_web"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.profile) || oldSelf.profile == self.profile",message="profile is immutable and cannot be changed once set"
type BundleEndpointConfig struct {
	// profile specifies the authentication profile used by the federation bundle endpoint.
	// "https_spiffe" uses SPIFFE-based mutual TLS authentication where the endpoint
	// presents a SPIFFE SVID for identity verification, suitable for environments
	// where all peers are SPIRE-managed.
	// "https_web" uses standard Web PKI (X.509 certificates from a public CA or ACME),
	// suitable for environments with mixed PKI infrastructure.
	// +kubebuilder:validation:Enum=https_spiffe;https_web
	// +kubebuilder:default=https_spiffe
	// +kubebuilder:validation:Required
	Profile BundleEndpointProfile `json:"profile"`

	// refreshHint is the suggested interval in seconds at which remote peers should
	// re-fetch the trust bundle from this endpoint. This value is included as a hint
	// in the bundle endpoint response. Must be between 60 and 3600 seconds.
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=3600
	// +kubebuilder:default=300
	// +kubebuilder:validation:Optional
	RefreshHint int32 `json:"refreshHint,omitempty"`

	// httpsWeb configures the https_web profile authentication settings.
	// This field is required when profile is set to "https_web" and must not be set
	// when profile is "https_spiffe". It allows specifying either ACME-based automatic
	// certificate management or a manually managed serving certificate.
	// +kubebuilder:validation:Optional
	HttpsWeb *HttpsWebConfig `json:"httpsWeb,omitempty"`
}

// BundleEndpointProfile represents the authentication profile for the federation bundle endpoint.
// +kubebuilder:validation:Enum=https_spiffe;https_web
type BundleEndpointProfile string

const (
	// HttpsSpiffeProfile uses SPIFFE authentication for the bundle endpoint.
	// The endpoint presents a SPIFFE SVID and uses mutual TLS for authentication.
	HttpsSpiffeProfile BundleEndpointProfile = "https_spiffe"

	// HttpsWebProfile uses standard Web PKI (X.509 certificates from a public CA)
	// for the bundle endpoint.
	HttpsWebProfile BundleEndpointProfile = "https_web"
)

// HttpsWebConfig configures the https_web profile authentication for the federation
// bundle endpoint. Exactly one of acme or servingCert must be specified.
// +kubebuilder:validation:XValidation:rule="(has(self.acme) && !has(self.servingCert)) || (!has(self.acme) && has(self.servingCert))",message="exactly one of acme or servingCert must be set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.acme) || has(self.acme)",message="cannot switch from acme to servingCert configuration"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.servingCert) || has(self.servingCert)",message="cannot switch from servingCert to acme configuration"
type HttpsWebConfig struct {
	// acme configures automatic certificate management using the ACME protocol
	// (e.g., Let's Encrypt). When configured, the SPIRE Server obtains and renews
	// TLS certificates automatically. Mutually exclusive with servingCert.
	// +kubebuilder:validation:Optional
	Acme *AcmeConfig `json:"acme,omitempty"`

	// servingCert configures a manually managed TLS certificate from a Kubernetes Secret
	// for the federation bundle endpoint. Mutually exclusive with acme.
	// +kubebuilder:validation:Optional
	ServingCert *ServingCertConfig `json:"servingCert,omitempty"`
}

// AcmeConfig configures automatic TLS certificate provisioning using the ACME protocol.
type AcmeConfig struct {
	// directoryUrl is the ACME directory URL used to discover ACME endpoints
	// (e.g., "https://acme-v02.api.letsencrypt.org/directory" for Let's Encrypt production).
	// Must be a valid HTTPS URL.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https://.*`
	// +kubebuilder:validation:MaxLength=512
	DirectoryUrl string `json:"directoryUrl"`

	// domainName is the domain name for which the ACME certificate will be issued.
	// This should match the external DNS name of the federation bundle endpoint Route.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	DomainName string `json:"domainName"`

	// email is the email address used for ACME account registration and certificate
	// expiration notifications.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9][a-zA-Z0-9._%+-]*[a-zA-Z0-9]@[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*\.[a-zA-Z]{2,}$`
	// +kubebuilder:validation:MaxLength=254
	Email string `json:"email"`

	// tosAccepted indicates acceptance of the ACME provider's Terms of Service.
	// Certificate provisioning will not proceed unless this is set to "true".
	// +kubebuilder:default:="false"
	// +kubebuilder:validation:Enum:="true";"false"
	// +kubebuilder:validation:Optional
	TosAccepted string `json:"tosAccepted,omitempty"`
}

// ServingCertConfig configures TLS certificates for the federation bundle endpoint.
// The service CA certificate is always used for internal communication from the Route
// to the SPIRE server pod. For external communication from clients to the Route, the
// certificate is controlled by externalSecretRef.
type ServingCertConfig struct {
	// fileSyncInterval specifies how often, in seconds, the SPIRE server checks for
	// certificate file updates on disk. Must be between 3600 and 7776000 seconds
	// (1 hour to 90 days).
	// +kubebuilder:validation:Minimum=3600
	// +kubebuilder:validation:Maximum=7776000
	// +kubebuilder:default=86400
	// +kubebuilder:validation:Optional
	FileSyncInterval int32 `json:"fileSyncInterval,omitempty"`

	// externalSecretRef is the name of a Kubernetes Secret in the same namespace
	// as the operator and operands that contains the TLS certificate for the SPIRE
	// server federation Route host. The secret must contain tls.crt and tls.key fields.
	// The OpenShift Ingress Operator reads this secret to configure the Route's TLS
	// certificate for external access.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	ExternalSecretRef string `json:"externalSecretRef,omitempty"`
}

// FederatesWithConfig represents a remote trust domain that this cluster federates with.
// Each entry configures the local SPIRE Server to retrieve the trust bundle from a
// remote cluster's federation endpoint.
// +kubebuilder:validation:XValidation:rule="self.bundleEndpointProfile == 'https_spiffe' ? has(self.endpointSpiffeId) && self.endpointSpiffeId != '' : true",message="endpointSpiffeId is required when bundleEndpointProfile is https_spiffe"
type FederatesWithConfig struct {
	// trustDomain is the SPIFFE trust domain name of the remote cluster to federate with.
	// This must match the trust domain configured on the remote SPIRE Server.
	// Must contain only lowercase alphanumeric characters, dots, hyphens, and underscores.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9._-]{1,255}$`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	TrustDomain string `json:"trustDomain"`

	// bundleEndpointUrl is the HTTPS URL of the remote cluster's federation bundle endpoint.
	// The local SPIRE Server connects to this URL to retrieve the remote trust bundle
	// for federation trust establishment.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https://.*`
	// +kubebuilder:validation:MaxLength=512
	BundleEndpointUrl string `json:"bundleEndpointUrl"`

	// bundleEndpointProfile specifies the authentication profile of the remote federation
	// bundle endpoint. Must match the profile configured on the remote cluster's bundle endpoint.
	// "https_spiffe" requires endpointSpiffeId to be set for SPIFFE-based authentication.
	// "https_web" uses standard Web PKI authentication.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=https_spiffe;https_web
	BundleEndpointProfile BundleEndpointProfile `json:"bundleEndpointProfile"`

	// endpointSpiffeId is the expected SPIFFE ID of the remote federation bundle endpoint.
	// This field is required when bundleEndpointProfile is "https_spiffe" and is used to
	// verify the identity of the remote endpoint during mutual TLS authentication.
	// Must be a valid SPIFFE ID starting with "spiffe://".
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^spiffe://.*`
	// +kubebuilder:validation:MaxLength=2048
	EndpointSpiffeId string `json:"endpointSpiffeId,omitempty"`
}

// SpireServerStatus defines the observed state of spire-server related reconciliation made by operator
type SpireServerStatus struct {
	// conditions holds information of the current state of the spire-server resources.
	ConditionalStatus `json:",inline,omitempty"`
}

// GetConditionalStatus returns the conditional status of the SpireServer
func (s *SpireServer) GetConditionalStatus() ConditionalStatus {
	return s.Status.ConditionalStatus
}

// +kubebuilder:object:root=true
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SpireServerList contain the list of SpireServer
type SpireServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SpireServer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SpireServer{}, &SpireServerList{})
}
