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
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.spec.federation) || has(self.spec.federation)",message="spec.federation cannot be removed once set"
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

	// federation configures SPIRE federation endpoints and trust domain relationships.
	// When set, the operator manages federation bundle endpoint exposure and peer trust domain configuration.
	// Once federation is set, it cannot be removed; to disable federation the system must be
	// uninstalled and reinstalled. Peer configurations (federatesWith) remain dynamic and
	// can be added or removed at any time.
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

// FederationConfig defines federation bundle endpoint and federated trust domain configuration.
// It enables the SPIRE Server to expose a bundle endpoint and federate with remote trust domains.
type FederationConfig struct {
	// bundleEndpoint configures this cluster's federation bundle endpoint.
	// The federation endpoint is exposed on port 8443 and allows remote SPIRE Servers
	// to retrieve this cluster's trust bundle for federation.
	// +kubebuilder:validation:Required
	BundleEndpoint BundleEndpointConfig `json:"bundleEndpoint"`

	// federatesWith lists the remote trust domains this cluster federates with.
	// Each entry defines a remote SPIRE Server's bundle endpoint URL and authentication profile.
	// A maximum of 50 federated trust domains is supported.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	// +listType=map
	// +listMapKey=trustDomain
	FederatesWith []FederatesWithConfig `json:"federatesWith,omitempty"`

	// managedRoute controls whether the operator automatically creates an OpenShift Route
	// for the federation endpoint.
	// When set to "true", the operator creates and manages a Route to expose the federation
	// bundle endpoint externally. When set to "false", administrators must manually configure
	// custom Routes or ingress for the federation endpoint.
	// +kubebuilder:default:="true"
	// +kubebuilder:validation:Enum:="true";"false"
	// +kubebuilder:validation:Optional
	ManagedRoute string `json:"managedRoute,omitempty"`
}

// BundleEndpointConfig configures how this cluster exposes its federation bundle endpoint.
// The federation endpoint is exposed on 0.0.0.0:8443.
// +kubebuilder:validation:XValidation:rule="self.profile == 'https_web' ? has(self.httpsWeb) : true",message="httpsWeb is required when profile is https_web"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.profile) || oldSelf.profile == self.profile",message="profile is immutable and cannot be changed once set"
type BundleEndpointConfig struct {
	// profile is the bundle endpoint authentication profile.
	// The profile determines how remote SPIRE Servers authenticate when retrieving the trust bundle.
	// Valid values are "https_spiffe" for SPIFFE authentication (default) and "https_web" for Web PKI
	// using X.509 certificates from a public CA.
	// This field is immutable and cannot be changed once set.
	// +kubebuilder:validation:Enum=https_spiffe;https_web
	// +kubebuilder:default=https_spiffe
	Profile BundleEndpointProfile `json:"profile"`

	// refreshHint is the hint for bundle refresh interval in seconds.
	// Remote SPIRE Servers use this value to determine how frequently to poll for updated trust bundles.
	// Must be between 60 and 3600 seconds.
	// When omitted, this means the user has no opinion and the value is left to the platform
	// to choose a good default, which is subject to change over time. The current default is 300.
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=3600
	// +kubebuilder:default=300
	RefreshHint int32 `json:"refreshHint,omitempty"`

	// httpsWeb configures the https_web profile authentication.
	// This field is required when profile is "https_web" and must not be set when profile is "https_spiffe".
	// Exactly one of acme or servingCert must be specified within this configuration.
	// +kubebuilder:validation:Optional
	HttpsWeb *HttpsWebConfig `json:"httpsWeb,omitempty"`
}

// BundleEndpointProfile represents the authentication profile for the federation bundle endpoint.
// +kubebuilder:validation:Enum=https_spiffe;https_web
type BundleEndpointProfile string

const (
	// HttpsSpiffeProfile uses SPIFFE authentication for the bundle endpoint.
	// Remote SPIRE Servers authenticate using their SPIFFE identity (SVID).
	HttpsSpiffeProfile BundleEndpointProfile = "https_spiffe"

	// HttpsWebProfile uses Web PKI (X.509 certificates from a public CA) for the bundle endpoint.
	// Remote SPIRE Servers authenticate using standard TLS with publicly trusted certificates.
	HttpsWebProfile BundleEndpointProfile = "https_web"
)

// HttpsWebConfig configures https_web profile authentication for the federation bundle endpoint.
// Exactly one of acme or servingCert must be set.
// +kubebuilder:validation:XValidation:rule="(has(self.acme) && !has(self.servingCert)) || (!has(self.acme) && has(self.servingCert))",message="exactly one of acme or servingCert must be set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.acme) || has(self.acme)",message="cannot switch from acme to servingCert configuration"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.servingCert) || has(self.servingCert)",message="cannot switch from servingCert to acme configuration"
type HttpsWebConfig struct {
	// acme configures automatic certificate management using the ACME protocol.
	// When set, the SPIRE Server automatically obtains and renews TLS certificates from an ACME provider
	// (e.g., Let's Encrypt). This field is mutually exclusive with servingCert.
	// +kubebuilder:validation:Optional
	Acme *AcmeConfig `json:"acme,omitempty"`

	// servingCert configures the TLS certificate from a Kubernetes Secret for the federation endpoint.
	// When set, the administrator provides and manages the TLS certificate manually.
	// This field is mutually exclusive with acme.
	// +kubebuilder:validation:Optional
	ServingCert *ServingCertConfig `json:"servingCert,omitempty"`
}

// AcmeConfig configures ACME protocol-based automatic certificate provisioning
// for the federation bundle endpoint.
type AcmeConfig struct {
	// directoryUrl is the ACME directory URL for certificate provisioning.
	// Must be a valid HTTPS URL pointing to an ACME-compliant CA directory
	// (e.g., "https://acme-v02.api.letsencrypt.org/directory").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https://.*`
	// +kubebuilder:validation:MaxLength=512
	DirectoryUrl string `json:"directoryUrl"`

	// domainName is the domain name for the ACME certificate.
	// This should match the externally accessible hostname of the federation endpoint.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	DomainName string `json:"domainName"`

	// email is the email address for ACME account registration.
	// The ACME provider uses this email for certificate expiration notifications
	// and account recovery.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`
	// +kubebuilder:validation:MaxLength=320
	Email string `json:"email"`

	// tosAccepted indicates acceptance of the ACME provider's Terms of Service.
	// Must be set to "true" to enable ACME certificate provisioning.
	// +kubebuilder:default:="false"
	// +kubebuilder:validation:Enum:="true";"false"
	// +kubebuilder:validation:Optional
	TosAccepted string `json:"tosAccepted,omitempty"`
}

// ServingCertConfig configures TLS certificates for the federation endpoint.
// The service CA certificate is always used for internal communication from the Route to the
// SPIRE Server pod. For external communication from clients to the Route, the certificate is
// controlled by externalSecretRef.
type ServingCertConfig struct {
	// fileSyncInterval is how often the SPIRE Server checks for certificate file updates, in seconds.
	// This controls the frequency at which the server reloads the TLS certificate from disk.
	// Must be between 3600 and 7776000 seconds (1 hour to 90 days).
	// When omitted, this means the user has no opinion and the value is left to the platform
	// to choose a good default, which is subject to change over time. The current default is 86400 (24 hours).
	// +kubebuilder:validation:Minimum=3600
	// +kubebuilder:validation:Maximum=7776000
	// +kubebuilder:default=86400
	FileSyncInterval int32 `json:"fileSyncInterval,omitempty"`

	// externalSecretRef is the name of an externally managed Secret that contains the TLS certificate
	// for the SPIRE Server federation Route host. The Secret must be in the same namespace where the
	// operator and operands are deployed and must contain tls.crt and tls.key fields. The OpenShift
	// Ingress Operator reads this Secret to configure the Route's TLS certificate.
	// Must be a valid Kubernetes resource name.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	ExternalSecretRef string `json:"externalSecretRef,omitempty"`
}

// FederatesWithConfig represents a remote trust domain to federate with.
// Each entry configures how this SPIRE Server connects to a remote SPIRE Server's
// bundle endpoint to retrieve its trust bundle.
// +kubebuilder:validation:XValidation:rule="self.bundleEndpointProfile == 'https_spiffe' ? has(self.endpointSpiffeId) && self.endpointSpiffeId != '' : true",message="endpointSpiffeId is required when bundleEndpointProfile is https_spiffe"
type FederatesWithConfig struct {
	// trustDomain is the SPIFFE trust domain name of the remote cluster.
	// This uniquely identifies the remote trust domain in the federation relationship.
	// Must be a valid trust domain name consisting of lowercase alphanumeric characters,
	// dots, hyphens, and underscores.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-z0-9._-]{1,255}$`
	TrustDomain string `json:"trustDomain"`

	// bundleEndpointUrl is the URL of the remote SPIRE Server's federation bundle endpoint.
	// Must be a valid HTTPS URL pointing to the remote cluster's federation endpoint.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^https://.*`
	// +kubebuilder:validation:MaxLength=512
	BundleEndpointUrl string `json:"bundleEndpointUrl"`

	// bundleEndpointProfile is the authentication profile of the remote federation endpoint.
	// Must match the profile configured on the remote SPIRE Server's bundle endpoint.
	// Valid values are "https_spiffe" for SPIFFE authentication and "https_web" for Web PKI.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=https_spiffe;https_web
	BundleEndpointProfile BundleEndpointProfile `json:"bundleEndpointProfile"`

	// endpointSpiffeId is the expected SPIFFE ID of the remote SPIRE Server.
	// This field is required when bundleEndpointProfile is "https_spiffe" and must not be set
	// when bundleEndpointProfile is "https_web". The SPIFFE ID is used to authenticate the
	// remote endpoint during TLS handshake.
	// Must be a valid SPIFFE ID starting with "spiffe://".
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^spiffe://.*`
	// +kubebuilder:validation:MaxLength=512
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
