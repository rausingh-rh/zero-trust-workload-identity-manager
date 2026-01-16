#!/bin/bash

set -e

# Script to patch SpireServer with external TLS certificate for federation endpoint
# This should be run AFTER:
#   1. Initial SpireServer deployment (via setup script)
#   2. cert-manager operator is installed
#   3. ACME Issuer is created
#   4. Certificate CR is created and cert-manager has issued the certificate

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Usage function
usage() {
    cat << EOF
Usage: $0 -k <kubeconfig> -s <secret-name> [-i <sync-interval>] [--skip-rbac]

This script patches the SpireServer CR to add an external TLS certificate reference
for the federation endpoint Route. It also creates necessary RBAC for the ingress router.

Options:
    -k, --kubeconfig       Path to kubeconfig
    -s, --secret-name      Name of the secret containing tls.crt and tls.key (managed by cert-manager)
                          Default: spire-server-federation-tls
    -i, --interval         File sync interval in seconds (default: 86400 = 24 hours)
                          Must be between 3600 (1 hour) and 7776000 (90 days)
    --skip-rbac           Skip RBAC creation (if already created manually)
    -h, --help            Display this help message

Prerequisites:
    1. cert-manager operator must be installed on the cluster
    2. ACME Issuer must be created in zero-trust-workload-identity-manager namespace
    3. Certificate CR must be created and ready
    4. The SpireServer CR must already be deployed with https_web profile

Complete workflow example:
    # Step 1: Deploy operator and run federation setup script
    ./setup-3-cluster-federation-serving_cert.sh \\
      -k1 /path/to/kubeconfig1 -k2 /path/to/kubeconfig2 -k3 /path/to/kubeconfig3 \\
      -c1 cluster1 -c2 cluster2 -c3 cluster3

    # Step 2: Install cert-manager operator
    oc apply -f - <<EOF
    apiVersion: v1
    kind: Namespace
    metadata:
      name: cert-manager-operator
    ---
    apiVersion: operators.coreos.com/v1
    kind: OperatorGroup
    metadata:
      name: openshift-cert-manager-operator
      namespace: cert-manager-operator
    spec:
      upgradeStrategy: Default
    ---
    apiVersion: operators.coreos.com/v1alpha1
    kind: Subscription
    metadata:
      name: openshift-cert-manager-operator
      namespace: cert-manager-operator
    spec:
      source: redhat-operators
      sourceNamespace: openshift-marketplace
      name: openshift-cert-manager-operator
      channel: stable-v1
    EOF

    # Step 3: Create ACME Issuer (Let's Encrypt)
    oc apply -f - <<EOF
    apiVersion: cert-manager.io/v1
    kind: Issuer
    metadata:
      name: letsencrypt-http01
      namespace: zero-trust-workload-identity-manager
    spec:
      acme:
        server: https://acme-v02.api.letsencrypt.org/directory
        privateKeySecretRef:
          name: letsencrypt-account-key
        solvers:
          - http01:
              ingress:
                ingressClassName: openshift-default
    EOF

    # Step 4: Create Certificate CR (replace domain with your actual federation endpoint)
    export TLS_SECRET_NAME=spire-server-federation-tls
    oc apply -f - <<EOF
    apiVersion: cert-manager.io/v1
    kind: Certificate
    metadata:
      name: \$TLS_SECRET_NAME
      namespace: zero-trust-workload-identity-manager
    spec:
      secretName: \$TLS_SECRET_NAME
      commonName: federation.apps.cluster3.example.com
      dnsNames:
        - federation.apps.cluster3.example.com
      usages:
        - server auth
      issuerRef:
        kind: Issuer
        name: letsencrypt-http01
    EOF

    # Step 5: Wait for certificate to be ready, then run this script
    $0 -k /path/to/kubeconfig -s spire-server-federation-tls

EOF
    exit 1
}

# Parse arguments
KUBECONFIG_PATH=""
SECRET_NAME="spire-server-federation-tls"
SYNC_INTERVAL=86400
SKIP_RBAC=false

while [[ $# -gt 0 ]]; do
    case $1 in
        -k|--kubeconfig)
            KUBECONFIG_PATH="$2"
            shift 2
            ;;
        -s|--secret-name)
            SECRET_NAME="$2"
            shift 2
            ;;
        -i|--interval)
            SYNC_INTERVAL="$2"
            shift 2
            ;;
        --skip-rbac)
            SKIP_RBAC=true
            shift
            ;;
        -h|--help)
            usage
            ;;
        *)
            print_error "Unknown option: $1"
            usage
            ;;
    esac
done

# Validate required arguments
if [[ -z "$KUBECONFIG_PATH" ]]; then
    print_error "Kubeconfig is required"
    usage
fi

if [[ ! -f "$KUBECONFIG_PATH" ]]; then
    print_error "Kubeconfig file not found: $KUBECONFIG_PATH"
    exit 1
fi

# Validate sync interval
if [[ $SYNC_INTERVAL -lt 3600 || $SYNC_INTERVAL -gt 7776000 ]]; then
    print_error "Sync interval must be between 3600 (1 hour) and 7776000 (90 days)"
    exit 1
fi

print_info "=========================================="
print_info "Patching SpireServer with External TLS Cert"
print_info "=========================================="
print_info ""
print_info "Kubeconfig: $KUBECONFIG_PATH"
print_info "Secret Name: $SECRET_NAME"
print_info "Sync Interval: $SYNC_INTERVAL seconds"
print_info "Skip RBAC: $SKIP_RBAC"
print_info ""

# Check if cert-manager is installed
print_info "Checking if cert-manager is installed..."
if ! oc --kubeconfig="$KUBECONFIG_PATH" get namespace cert-manager &>/dev/null && \
   ! oc --kubeconfig="$KUBECONFIG_PATH" get namespace cert-manager-operator &>/dev/null; then
    print_warn "cert-manager namespace not found"
    print_warn "cert-manager operator should be installed. Continuing anyway..."
fi

# Check if Certificate CR exists
print_info "Checking for Certificate CR..."
CERT_NAME=$(oc --kubeconfig="$KUBECONFIG_PATH" -n zero-trust-workload-identity-manager get certificate -o name 2>/dev/null | grep "$SECRET_NAME" | head -1 || echo "")
if [[ -n "$CERT_NAME" ]]; then
    print_info "Found Certificate: $CERT_NAME"
    
    # Check certificate status
    CERT_READY=$(oc --kubeconfig="$KUBECONFIG_PATH" -n zero-trust-workload-identity-manager get "$CERT_NAME" -o jsonpath='{.status.conditions[?(@.type=="Ready")].status}' 2>/dev/null || echo "")
    if [[ "$CERT_READY" == "True" ]]; then
        print_info "Certificate is ready"
    else
        print_warn "Certificate is not ready yet (status: $CERT_READY)"
        print_warn "Waiting for certificate to be issued by cert-manager..."
        print_warn "You can check status with: oc describe $CERT_NAME -n zero-trust-workload-identity-manager"
    fi
else
    print_warn "Certificate CR not found with secretName: $SECRET_NAME"
    print_warn "The Certificate CR should be created before running this script"
fi

# Check if secret exists
print_info "Checking if secret exists..."
if ! oc --kubeconfig="$KUBECONFIG_PATH" -n zero-trust-workload-identity-manager get secret "$SECRET_NAME" &>/dev/null; then
    print_error "Secret '$SECRET_NAME' not found in zero-trust-workload-identity-manager namespace"
    print_error ""
    print_error "The secret should be created by cert-manager via a Certificate resource."
    print_error "Please ensure:"
    print_error "  1. cert-manager operator is installed on the cluster"
    print_error "  2. ACME Issuer is created in zero-trust-workload-identity-manager namespace"
    print_error "  3. Certificate CR is created that references this secret name"
    print_error "  4. cert-manager has successfully issued the certificate"
    print_error ""
    print_error "Check Certificate status:"
    print_error "  oc get certificate -n zero-trust-workload-identity-manager"
    print_error "  oc describe certificate $SECRET_NAME -n zero-trust-workload-identity-manager"
    print_error ""
    print_error "Check cert-manager logs:"
    print_error "  oc logs -n cert-manager -l app=cert-manager"
    print_error "  oc logs -n cert-manager-operator -l app=cert-manager-operator"
    exit 1
fi

print_info "Secret '$SECRET_NAME' found"

# Check if secret has required fields
print_info "Validating secret fields..."
if ! oc --kubeconfig="$KUBECONFIG_PATH" -n zero-trust-workload-identity-manager get secret "$SECRET_NAME" -o jsonpath='{.data.tls\.crt}' | grep -q .; then
    print_error "Secret '$SECRET_NAME' is missing 'tls.crt' field"
    exit 1
fi

if ! oc --kubeconfig="$KUBECONFIG_PATH" -n zero-trust-workload-identity-manager get secret "$SECRET_NAME" -o jsonpath='{.data.tls\.key}' | grep -q .; then
    print_error "Secret '$SECRET_NAME' is missing 'tls.key' field"
    exit 1
fi

print_info "Secret validation passed"

# Check if SpireServer exists
print_info "Checking if SpireServer CR exists..."
if ! oc --kubeconfig="$KUBECONFIG_PATH" get spireserver cluster &>/dev/null; then
    print_error "SpireServer 'cluster' not found"
    print_error "Please deploy the SpireServer CR first"
    exit 1
fi

print_info "SpireServer 'cluster' found"

# Check if it's using https_web profile
print_info "Verifying https_web profile..."
PROFILE=$(oc --kubeconfig="$KUBECONFIG_PATH" get spireserver cluster -o jsonpath='{.spec.federation.bundleEndpoint.profile}' 2>/dev/null || echo "")
if [[ "$PROFILE" != "https_web" ]]; then
    print_error "SpireServer is not configured with https_web profile (current: $PROFILE)"
    print_error "This patch is only applicable for https_web profile with serving cert"
    exit 1
fi

print_info "Profile verification passed"

# Create RBAC for ingress router to access the secret
if [[ "$SKIP_RBAC" == "false" ]]; then
    print_info ""
    print_info "Creating RBAC for ingress router to access the secret..."
    
    # Check if role exists
    if oc --kubeconfig="$KUBECONFIG_PATH" -n zero-trust-workload-identity-manager get role secret-reader &>/dev/null; then
        print_info "Role 'secret-reader' already exists, deleting and recreating..."
        oc --kubeconfig="$KUBECONFIG_PATH" -n zero-trust-workload-identity-manager delete role secret-reader &>/dev/null || true
    fi
    
    # Create role
    oc --kubeconfig="$KUBECONFIG_PATH" create role secret-reader \
        --verb=get,list,watch \
        --resource=secrets \
        --resource-name="$SECRET_NAME" \
        -n zero-trust-workload-identity-manager
    
    if [[ $? -eq 0 ]]; then
        print_info "Role 'secret-reader' created successfully"
    else
        print_error "Failed to create role 'secret-reader'"
        exit 1
    fi
    
    # Check if rolebinding exists
    if oc --kubeconfig="$KUBECONFIG_PATH" -n zero-trust-workload-identity-manager get rolebinding secret-reader-binding &>/dev/null; then
        print_info "RoleBinding 'secret-reader-binding' already exists, deleting and recreating..."
        oc --kubeconfig="$KUBECONFIG_PATH" -n zero-trust-workload-identity-manager delete rolebinding secret-reader-binding &>/dev/null || true
    fi
    
    # Create rolebinding
    oc --kubeconfig="$KUBECONFIG_PATH" create rolebinding secret-reader-binding \
        --role=secret-reader \
        --serviceaccount=openshift-ingress:router \
        -n zero-trust-workload-identity-manager
    
    if [[ $? -eq 0 ]]; then
        print_info "RoleBinding 'secret-reader-binding' created successfully"
    else
        print_error "Failed to create rolebinding 'secret-reader-binding'"
        exit 1
    fi
else
    print_info "Skipping RBAC creation (--skip-rbac flag set)"
fi

# Apply the patch
print_info ""
print_info "Applying patch to SpireServer..."

cat <<EOF | oc --kubeconfig="$KUBECONFIG_PATH" patch spireserver cluster --type=merge -p "$(cat)"
spec:
  federation:
    bundleEndpoint:
      httpsWeb:
        servingCert:
          externalSecretRef: "$SECRET_NAME"
          fileSyncInterval: $SYNC_INTERVAL
EOF

if [[ $? -eq 0 ]]; then
    print_info ""
    print_info "=========================================="
    print_info "Configuration completed successfully!"
    print_info "=========================================="
    print_info ""
    print_info "✓ RBAC created for ingress router (Role: secret-reader, RoleBinding: secret-reader-binding)"
    print_info "✓ SpireServer patched with external TLS certificate reference"
    print_info ""
    print_info "Configuration details:"
    print_info "  Secret Name: $SECRET_NAME"
    print_info "  Certificate sync interval: $SYNC_INTERVAL seconds"
    print_info "  cert-manager will automatically renew the certificate"
    print_info ""
    print_info "The federation endpoint Route will now use the Let's Encrypt certificate"
    print_info "from secret '$SECRET_NAME' for external TLS communication."
    print_info ""
    print_info "Verify the configuration:"
    print_info "  # Check SpireServer CR"
    print_info "  oc --kubeconfig=$KUBECONFIG_PATH get spireserver cluster -o yaml"
    print_info ""
    print_info "  # Check Certificate status"
    print_info "  oc --kubeconfig=$KUBECONFIG_PATH get certificate -n zero-trust-workload-identity-manager"
    print_info "  oc --kubeconfig=$KUBECONFIG_PATH describe certificate $SECRET_NAME -n zero-trust-workload-identity-manager"
    print_info ""
    print_info "  # Check the secret"
    print_info "  oc --kubeconfig=$KUBECONFIG_PATH describe secret $SECRET_NAME -n zero-trust-workload-identity-manager"
    print_info ""
    print_info "  # Check RBAC"
    print_info "  oc --kubeconfig=$KUBECONFIG_PATH get role,rolebinding -n zero-trust-workload-identity-manager | grep secret-reader"
    print_info ""
    print_info "  # Check the federation Route"
    print_info "  oc --kubeconfig=$KUBECONFIG_PATH get route spire-server-federation -n zero-trust-workload-identity-manager -o yaml"
    print_info ""
    print_info "  # Test the federation endpoint"
    print_info "  FEDERATION_URL=\$(oc --kubeconfig=$KUBECONFIG_PATH get route spire-server-federation -n zero-trust-workload-identity-manager -o jsonpath='{.spec.host}')"
    print_info "  curl -v https://\$FEDERATION_URL"
    print_info ""
    print_info "Monitor SPIRE server logs:"
    print_info "  oc --kubeconfig=$KUBECONFIG_PATH logs -n zero-trust-workload-identity-manager spire-server-0 -c spire-server -f"
else
    print_error "Failed to apply patch"
    exit 1
fi