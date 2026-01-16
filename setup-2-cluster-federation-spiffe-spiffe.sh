#!/bin/bash

set -e

# 2-Cluster Federation Setup Script: https_spiffe + https_spiffe
# This script sets up SPIRE federation across 2 OpenShift clusters:
#   - Cluster 1: https_spiffe profile
#   - Cluster 2: https_spiffe profile
#
# Prerequisites:
#   1. Deploy the Zero Trust Workload Identity Manager operator on both clusters
#   2. Ensure you have kubeconfig files for both clusters
#   3. Ensure 'oc' and 'curl' CLI tools are installed and accessible
#   4. Network connectivity to access federation endpoints (federation.<trust-domain>)
#
# Usage:
#   # Apply mode (actually apply to clusters):
#   ./setup-2-cluster-federation-spiffe-spiffe.sh \
#     -k1 /path/to/kubeconfig1 -k2 /path/to/kubeconfig2 \
#     -c1 cluster1-name -c2 cluster2-name \
#     --mode apply
#
#   # Print-only mode (just show YAMLs for console UI):
#   ./setup-2-cluster-federation-spiffe-spiffe.sh \
#     -k1 /path/to/kubeconfig1 -k2 /path/to/kubeconfig2 \
#     -c1 cluster1-name -c2 cluster2-name \
#     --mode print-only

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

print_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

print_section() {
    echo -e "${BLUE}[SECTION]${NC} $1"
}

# Function to display usage
usage() {
    cat << EOF
Usage: $0 -k1 <kubeconfig1> -k2 <kubeconfig2> -c1 <cluster1-name> -c2 <cluster2-name> --mode <apply|print-only>

Options:
    -k1, --kubeconfig1      Path to kubeconfig for cluster 1 (https_spiffe)
    -k2, --kubeconfig2      Path to kubeconfig for cluster 2 (https_spiffe)
    -c1, --cluster1-name    Name for cluster 1
    -c2, --cluster2-name    Name for cluster 2
    -m,  --mode            Mode: 'apply' to apply changes, 'print-only' to print YAMLs
    -h,  --help            Display this help message

Example (Apply mode):
    $0 -k1 /path/to/kubeconfig1 -k2 /path/to/kubeconfig2 \\
       -c1 cluster1 -c2 cluster2 --mode apply

Example (Print-only mode):
    $0 -k1 /path/to/kubeconfig1 -k2 /path/to/kubeconfig2 \\
       -c1 cluster1 -c2 cluster2 --mode print-only

EOF
    exit 1
}

# Parse command line arguments
KUBECONFIG1=""
KUBECONFIG2=""
CLUSTER1_NAME=""
CLUSTER2_NAME=""
MODE="apply"

while [[ $# -gt 0 ]]; do
    case $1 in
        -k1|--kubeconfig1)
            KUBECONFIG1="$2"
            shift 2
            ;;
        -k2|--kubeconfig2)
            KUBECONFIG2="$2"
            shift 2
            ;;
        -c1|--cluster1-name)
            CLUSTER1_NAME="$2"
            shift 2
            ;;
        -c2|--cluster2-name)
            CLUSTER2_NAME="$2"
            shift 2
            ;;
        -m|--mode)
            MODE="$2"
            shift 2
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
if [[ -z "$KUBECONFIG1" || -z "$KUBECONFIG2" ]]; then
    print_error "Both kubeconfig paths are required"
    usage
fi

if [[ -z "$CLUSTER1_NAME" || -z "$CLUSTER2_NAME" ]]; then
    print_error "Both cluster names are required"
    usage
fi

if [[ "$MODE" != "apply" && "$MODE" != "print-only" ]]; then
    print_error "Mode must be either 'apply' or 'print-only'"
    usage
fi

# Validate kubeconfig files exist
for kc in "$KUBECONFIG1" "$KUBECONFIG2"; do
    if [[ ! -f "$kc" ]]; then
        print_error "Kubeconfig file not found: $kc"
        exit 1
    fi
done

# Create temporary directory for bundle files
TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

print_info "Using temporary directory: $TEMP_DIR"
print_info "Running in $MODE mode"

# Function to get app domain from cluster
get_app_domain() {
    local kubeconfig=$1
    oc --kubeconfig="$kubeconfig" get dns cluster -o jsonpath='{ .spec.baseDomain }' 2>/dev/null || echo ""
}

# Function to wait for SPIRE server to be ready
wait_for_spire_server() {
    local kubeconfig=$1
    local cluster_name=$2
    local max_wait=300
    local elapsed=0
    
    print_info "Waiting for SPIRE server to be ready on $cluster_name..."
    
    while [[ $elapsed -lt $max_wait ]]; do
        if oc --kubeconfig="$kubeconfig" -n zero-trust-workload-identity-manager get pod spire-server-0 &>/dev/null; then
            if oc --kubeconfig="$kubeconfig" -n zero-trust-workload-identity-manager wait --for=condition=ready pod/spire-server-0 --timeout=10s &>/dev/null; then
                print_info "SPIRE server is ready on $cluster_name"
                return 0
            fi
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done
    
    print_error "Timeout waiting for SPIRE server on $cluster_name"
    return 1
}

# Get app domains for both clusters
print_info "Retrieving cluster domains..."
BASE_DOMAIN1=$(get_app_domain "$KUBECONFIG1")
BASE_DOMAIN2=$(get_app_domain "$KUBECONFIG2")

if [[ -z "$BASE_DOMAIN1" || -z "$BASE_DOMAIN2" ]]; then
    print_error "Failed to retrieve base domains from one or more clusters"
    exit 1
fi

APP_DOMAIN1="apps.${BASE_DOMAIN1}"
APP_DOMAIN2="apps.${BASE_DOMAIN2}"

print_info "Cluster 1 ($CLUSTER1_NAME): $APP_DOMAIN1"
print_info "Cluster 2 ($CLUSTER2_NAME): $APP_DOMAIN2"

# Function to generate or apply SPIRE resources for cluster 1
deploy_spire_cluster1() {
    local JWT_ISSUER="oidc-discovery.${APP_DOMAIN1}"
    
    local yaml_content=$(cat <<EOF
apiVersion: operator.openshift.io/v1alpha1
kind: ZeroTrustWorkloadIdentityManager
metadata:
  name: cluster
spec:
  trustDomain: $APP_DOMAIN1
  clusterName: $CLUSTER1_NAME
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireServer
metadata:
  name: cluster
spec:
  caSubject:
    commonName: $APP_DOMAIN1
    country: "US"
    organization: "RH"
  persistence:
    size: "2Gi"
    accessMode: ReadWriteOncePod
  datastore:
    databaseType: sqlite3
    connectionString: "/run/spire/data/datastore.sqlite3"
    maxOpenConns: 100
    maxIdleConns: 2
    connMaxLifetime: 3600
  federation:
    bundleEndpoint:
      profile: https_spiffe
    managedRoute: "true"
    federatesWith:
    - trustDomain: $APP_DOMAIN2
      bundleEndpointUrl: https://federation.$APP_DOMAIN2
      bundleEndpointProfile: https_spiffe
      endpointSpiffeId: spiffe://$APP_DOMAIN2/spire/server
  jwtIssuer: https://$JWT_ISSUER
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireAgent
metadata:
  name: cluster
spec:
  nodeAttestor:
    k8sPSATEnabled: "true"
  workloadAttestors:
    k8sEnabled: "true"
    workloadAttestorsVerification:
      type: "auto"
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpiffeCSIDriver
metadata:
  name: cluster
spec: {}
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireOIDCDiscoveryProvider
metadata:
  name: cluster
spec:
  jwtIssuer: https://$JWT_ISSUER
EOF
)
    
    if [[ "$MODE" == "print-only" ]]; then
        print_section "=== Cluster 1 ($CLUSTER1_NAME) - SPIRE Resources ==="
        echo "$yaml_content"
        echo ""
    else
        print_info "Deploying SPIRE resources on Cluster 1 ($CLUSTER1_NAME)..."
        echo "$yaml_content" | oc --kubeconfig="$KUBECONFIG1" apply -f -
        print_info "Cluster 1 resources deployed successfully"
    fi
}

# Function to generate or apply SPIRE resources for cluster 2
deploy_spire_cluster2() {
    local JWT_ISSUER="oidc-discovery.${APP_DOMAIN2}"
    
    local yaml_content=$(cat <<EOF
apiVersion: operator.openshift.io/v1alpha1
kind: ZeroTrustWorkloadIdentityManager
metadata:
  name: cluster
spec:
  trustDomain: $APP_DOMAIN2
  clusterName: $CLUSTER2_NAME
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireServer
metadata:
  name: cluster
spec:
  caSubject:
    commonName: $APP_DOMAIN2
    country: "US"
    organization: "RH"
  persistence:
    size: "2Gi"
    accessMode: ReadWriteOncePod
  datastore:
    databaseType: sqlite3
    connectionString: "/run/spire/data/datastore.sqlite3"
    maxOpenConns: 100
    maxIdleConns: 2
    connMaxLifetime: 3600
  federation:
    bundleEndpoint:
      profile: https_spiffe
    managedRoute: "true"
    federatesWith:
    - trustDomain: $APP_DOMAIN1
      bundleEndpointUrl: https://federation.$APP_DOMAIN1
      bundleEndpointProfile: https_spiffe
      endpointSpiffeId: spiffe://$APP_DOMAIN1/spire/server
  jwtIssuer: https://$JWT_ISSUER
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireAgent
metadata:
  name: cluster
spec:
  nodeAttestor:
    k8sPSATEnabled: "true"
  workloadAttestors:
    k8sEnabled: "true"
    workloadAttestorsVerification:
      type: "auto"
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpiffeCSIDriver
metadata:
  name: cluster
spec: {}
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireOIDCDiscoveryProvider
metadata:
  name: cluster
spec:
  jwtIssuer: https://$JWT_ISSUER
EOF
)
    
    if [[ "$MODE" == "print-only" ]]; then
        print_section "=== Cluster 2 ($CLUSTER2_NAME) - SPIRE Resources ==="
        echo "$yaml_content"
        echo ""
    else
        print_info "Deploying SPIRE resources on Cluster 2 ($CLUSTER2_NAME)..."
        echo "$yaml_content" | oc --kubeconfig="$KUBECONFIG2" apply -f -
        print_info "Cluster 2 resources deployed successfully"
    fi
}

# Function to wait for federation endpoint to be ready
wait_for_federation_endpoint() {
    local bundle_endpoint_url=$1
    local cluster_name=$2
    local max_wait=300
    local elapsed=0
    
    print_info "Waiting for federation endpoint to be ready on $cluster_name..."
    print_info "  Checking: $bundle_endpoint_url"
    
    while [[ $elapsed -lt $max_wait ]]; do
        if curl -sSLk --max-time 10 --fail "$bundle_endpoint_url" -o /dev/null 2>/dev/null; then
            print_info "Federation endpoint is ready on $cluster_name"
            return 0
        fi
        sleep 5
        elapsed=$((elapsed + 5))
    done
    
    print_error "Timeout waiting for federation endpoint on $cluster_name"
    return 1
}

# Function to extract bundle from federation endpoint using curl
extract_bundle() {
    local bundle_endpoint_url=$1
    local cluster_name=$2
    local output_file=$3
    
    print_info "Fetching bundle from $cluster_name federation endpoint: $bundle_endpoint_url..."
    
    if curl -sSLk --max-time 30 --fail "$bundle_endpoint_url" -o "$output_file" 2>/dev/null; then
        if [[ -s "$output_file" ]]; then
            print_info "Bundle fetched successfully from $cluster_name"
            return 0
        fi
    fi
    
    print_error "Failed to fetch bundle from $cluster_name federation endpoint"
    return 1
}

# Function to create federated trust domain
create_federation() {
    local kubeconfig=$1
    local cluster_name=$2
    local trust_domain=$3
    local bundle_endpoint=$4
    local endpoint_spiffe_id=$5
    local bundle_file=$6
    local federation_name=$7
    
    local bundle_content
    bundle_content=$(cat "$bundle_file" | sed 's/^/    /')
    
    local yaml_content=$(cat <<EOF
apiVersion: spire.spiffe.io/v1alpha1
kind: ClusterFederatedTrustDomain
metadata:
  name: $federation_name
spec:
  trustDomain: $trust_domain
  bundleEndpointURL: $bundle_endpoint
  bundleEndpointProfile:
    type: https_spiffe
    endpointSPIFFEID: $endpoint_spiffe_id
  className: zero-trust-workload-identity-manager-spire
  trustDomainBundle: |-
$bundle_content
EOF
)
    
    if [[ "$MODE" == "print-only" ]]; then
        print_section "=== $cluster_name - Federation: $federation_name ==="
        echo "$yaml_content"
        echo ""
    else
        print_info "Creating federation $federation_name on $cluster_name..."
        echo "$yaml_content" | oc --kubeconfig="$kubeconfig" apply -f -
        print_info "Federation $federation_name created successfully"
    fi
}

# Main execution
print_info "=========================================="
print_info "2-Cluster Federation Setup"
print_info "Profile: https_spiffe + https_spiffe"
print_info "Mode: $MODE"
print_info "=========================================="
print_info ""

if [[ "$MODE" == "print-only" ]]; then
    print_info "=========================================="
    print_info "YAML Resources for Console UI"
    print_info "=========================================="
    print_info ""
    print_info "Instructions:"
    print_info "1. Copy each YAML section below"
    print_info "2. Apply to the respective cluster using console UI"
    print_info "3. Wait for SPIRE servers to be ready"
    print_info "4. Fetch bundles manually using: curl -k https://federation.<trust-domain>"
    print_info "5. Apply ClusterFederatedTrustDomain resources (update trustDomainBundle with fetched bundles)"
    print_info ""
    
    # Generate YAMLs
    deploy_spire_cluster1
    deploy_spire_cluster2
    
    print_section "=== Manual Steps for Federation ==="
    echo "# After SPIRE servers are ready on both clusters, fetch bundles:"
    echo ""
    echo "# Fetch bundle from Cluster 1:"
    echo "curl -k https://federation.$APP_DOMAIN1 > cluster1-bundle.json"
    echo ""
    echo "# Fetch bundle from Cluster 2:"
    echo "curl -k https://federation.$APP_DOMAIN2 > cluster2-bundle.json"
    echo ""
    echo "# Then create ClusterFederatedTrustDomain resources with the fetched bundles:"
    echo ""
    
    # Create dummy bundle files for YAML generation
    echo '{"keys": []}' > "${TEMP_DIR}/cluster1-bundle.json"
    echo '{"keys": []}' > "${TEMP_DIR}/cluster2-bundle.json"
    
    print_section "=== Cluster 1 - Federation to Cluster 2 ==="
    echo "# Note: Replace trustDomainBundle content with actual bundle from cluster2-bundle.json"
    cat <<EOF
apiVersion: spire.spiffe.io/v1alpha1
kind: ClusterFederatedTrustDomain
metadata:
  name: cluster-12-federation
spec:
  trustDomain: $APP_DOMAIN2
  bundleEndpointURL: https://federation.$APP_DOMAIN2
  bundleEndpointProfile:
    type: https_spiffe
    endpointSPIFFEID: spiffe://$APP_DOMAIN2/spire/server
  className: zero-trust-workload-identity-manager-spire
  trustDomainBundle: |-
    # REPLACE WITH CONTENT FROM cluster2-bundle.json
EOF
    echo ""
    
    print_section "=== Cluster 2 - Federation to Cluster 1 ==="
    echo "# Note: Replace trustDomainBundle content with actual bundle from cluster1-bundle.json"
    cat <<EOF
apiVersion: spire.spiffe.io/v1alpha1
kind: ClusterFederatedTrustDomain
metadata:
  name: cluster-21-federation
spec:
  trustDomain: $APP_DOMAIN1
  bundleEndpointURL: https://federation.$APP_DOMAIN1
  bundleEndpointProfile:
    type: https_spiffe
    endpointSPIFFEID: spiffe://$APP_DOMAIN1/spire/server
  className: zero-trust-workload-identity-manager-spire
  trustDomainBundle: |-
    # REPLACE WITH CONTENT FROM cluster1-bundle.json
EOF
    echo ""
    
else
    # Apply mode
    
    # Step 1: Deploy SPIRE resources
    print_info "Step 1: Deploying SPIRE resources on both clusters..."
    deploy_spire_cluster1
    deploy_spire_cluster2
    print_info ""
    
    # Step 2: Wait for SPIRE servers to be ready
    print_info "Step 2: Waiting for SPIRE servers to be ready..."
    wait_for_spire_server "$KUBECONFIG1" "$CLUSTER1_NAME"
    wait_for_spire_server "$KUBECONFIG2" "$CLUSTER2_NAME"
    print_info ""
    
    # Step 3: Wait for federation endpoints to be accessible
    print_info "Step 3: Waiting for federation endpoints to be accessible..."
    FEDERATION_URL1="https://federation.$APP_DOMAIN1"
    FEDERATION_URL2="https://federation.$APP_DOMAIN2"
    
    wait_for_federation_endpoint "$FEDERATION_URL1" "$CLUSTER1_NAME"
    wait_for_federation_endpoint "$FEDERATION_URL2" "$CLUSTER2_NAME"
    print_info ""
    
    # Step 4: Extract bundles from federation endpoints
    print_info "Step 4: Fetching trust bundles from federation endpoints..."
    BUNDLE1="${TEMP_DIR}/cluster1-bundle.json"
    BUNDLE2="${TEMP_DIR}/cluster2-bundle.json"
    
    extract_bundle "$FEDERATION_URL1" "$CLUSTER1_NAME" "$BUNDLE1"
    extract_bundle "$FEDERATION_URL2" "$CLUSTER2_NAME" "$BUNDLE2"
    print_info ""
    
    # Step 5: Create federation relationships
    print_info "Step 5: Creating federation relationships..."
    
    # Cluster 1 federation (to cluster 2)
    create_federation "$KUBECONFIG1" "$CLUSTER1_NAME" "$APP_DOMAIN2" \
        "https://federation.$APP_DOMAIN2" \
        "spiffe://$APP_DOMAIN2/spire/server" "$BUNDLE2" \
        "cluster-12-federation"
    
    # Cluster 2 federation (to cluster 1)
    create_federation "$KUBECONFIG2" "$CLUSTER2_NAME" "$APP_DOMAIN1" \
        "https://federation.$APP_DOMAIN1" \
        "spiffe://$APP_DOMAIN1/spire/server" "$BUNDLE1" \
        "cluster-21-federation"
    
    print_info ""
    print_info "=========================================="
    print_info "Federation setup completed successfully!"
    print_info "=========================================="
    print_info ""
    print_info "Summary:"
    print_info "  Cluster 1 ($CLUSTER1_NAME): $APP_DOMAIN1 (https_spiffe)"
    print_info "  Cluster 2 ($CLUSTER2_NAME): $APP_DOMAIN2 (https_spiffe)"
    print_info ""
    print_info "Federation endpoints:"
    print_info "  - Cluster 1: https://federation.$APP_DOMAIN1"
    print_info "  - Cluster 2: https://federation.$APP_DOMAIN2"
    print_info ""
    print_info "Federation relationship established:"
    print_info "  - Cluster 1 ↔ Cluster 2"
    print_info ""
    print_info "Verify federation status:"
    print_info "  oc --kubeconfig=$KUBECONFIG1 get clusterfederatedtrustdomains"
    print_info "  oc --kubeconfig=$KUBECONFIG2 get clusterfederatedtrustdomains"
fi

