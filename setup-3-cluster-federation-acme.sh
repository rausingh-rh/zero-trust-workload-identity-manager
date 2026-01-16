#!/bin/bash

set -e

# 3-Cluster Federation Setup Script with ACME Configuration
# This script sets up SPIRE federation across 3 OpenShift clusters:
#   - Cluster 1: https_spiffe profile
#   - Cluster 2: https_spiffe profile
#   - Cluster 3: https_web profile with ACME (Let's Encrypt)
#
# Prerequisites:
#   1. Deploy the Zero Trust Workload Identity Manager operator on all 3 clusters
#   2. Ensure you have kubeconfig files for all 3 clusters
#   3. Ensure 'oc' and 'curl' CLI tools are installed and accessible
#   4. Network connectivity to access federation endpoints (federation.<trust-domain>)
#
#
# Usage:
#   ./setup-3-cluster-federation-acme.sh \
#     -k1 /path/to/kubeconfig1 -k2 /path/to/kubeconfig2 -k3 /path/to/kubeconfig3 \
#     -c1 cluster1-name -c2 cluster2-name -c3 cluster3-name \
#     [-e email@example.com]

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
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

# Function to display usage
usage() {
    cat << EOF
Usage: $0 -k1 <kubeconfig1> -k2 <kubeconfig2> -k3 <kubeconfig3> -c1 <cluster1-name> -c2 <cluster2-name> -c3 <cluster3-name> [-e <email>]

Options:
    -k1, --kubeconfig1      Path to kubeconfig for cluster 1 (https_spiffe)
    -k2, --kubeconfig2      Path to kubeconfig for cluster 2 (https_spiffe)
    -k3, --kubeconfig3      Path to kubeconfig for cluster 3 (https_web with ACME)
    -c1, --cluster1-name    Name for cluster 1
    -c2, --cluster2-name    Name for cluster 2
    -c3, --cluster3-name    Name for cluster 3
    -e,  --email           Email for ACME (Let's Encrypt) on cluster 3 (default: rausingh@redhat.com)
    -h,  --help            Display this help message

Example:
    $0 -k1 /path/to/kubeconfig1 -k2 /path/to/kubeconfig2 -k3 /path/to/kubeconfig3 \\
       -c1 aws14nov1 -c2 aws14nov2 -c3 aws14nov3 -e admin@example.com

EOF
    exit 1
}

# Parse command line arguments
KUBECONFIG1=""
KUBECONFIG2=""
KUBECONFIG3=""
CLUSTER1_NAME=""
CLUSTER2_NAME=""
CLUSTER3_NAME=""
ACME_EMAIL="rausingh@redhat.com"

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
        -k3|--kubeconfig3)
            KUBECONFIG3="$2"
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
        -c3|--cluster3-name)
            CLUSTER3_NAME="$2"
            shift 2
            ;;
        -e|--email)
            ACME_EMAIL="$2"
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
if [[ -z "$KUBECONFIG1" || -z "$KUBECONFIG2" || -z "$KUBECONFIG3" ]]; then
    print_error "All three kubeconfig paths are required"
    usage
fi

if [[ -z "$CLUSTER1_NAME" || -z "$CLUSTER2_NAME" || -z "$CLUSTER3_NAME" ]]; then
    print_error "All three cluster names are required"
    usage
fi

# Validate kubeconfig files exist
for kc in "$KUBECONFIG1" "$KUBECONFIG2" "$KUBECONFIG3"; do
    if [[ ! -f "$kc" ]]; then
        print_error "Kubeconfig file not found: $kc"
        exit 1
    fi
done

# Create temporary directory for bundle files
TEMP_DIR=$(mktemp -d)
trap "rm -rf $TEMP_DIR" EXIT

print_info "Using temporary directory: $TEMP_DIR"

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

# Get app domains for all clusters
print_info "Retrieving cluster domains..."
BASE_DOMAIN1=$(get_app_domain "$KUBECONFIG1")
BASE_DOMAIN2=$(get_app_domain "$KUBECONFIG2")
BASE_DOMAIN3=$(get_app_domain "$KUBECONFIG3")

if [[ -z "$BASE_DOMAIN1" || -z "$BASE_DOMAIN2" || -z "$BASE_DOMAIN3" ]]; then
    print_error "Failed to retrieve base domains from one or more clusters"
    exit 1
fi

APP_DOMAIN1="apps.${BASE_DOMAIN1}"
APP_DOMAIN2="apps.${BASE_DOMAIN2}"
APP_DOMAIN3="apps.${BASE_DOMAIN3}"

print_info "Cluster 1 ($CLUSTER1_NAME): $APP_DOMAIN1"
print_info "Cluster 2 ($CLUSTER2_NAME): $APP_DOMAIN2"
print_info "Cluster 3 ($CLUSTER3_NAME): $APP_DOMAIN3"

# Function to deploy SPIRE resources
deploy_spire_cluster1() {
    print_info "Deploying SPIRE resources on Cluster 1 ($CLUSTER1_NAME)..."
    
    local JWT_ISSUER="oidc-discovery.${APP_DOMAIN1}"
    
    cat <<EOF | oc --kubeconfig="$KUBECONFIG1" apply -f -
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
    - trustDomain: $APP_DOMAIN3
      bundleEndpointUrl: https://federation.$APP_DOMAIN3
      bundleEndpointProfile: https_web
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
    
    print_info "Cluster 1 resources deployed successfully"
}

deploy_spire_cluster2() {
    print_info "Deploying SPIRE resources on Cluster 2 ($CLUSTER2_NAME)..."
    
    local JWT_ISSUER="oidc-discovery.${APP_DOMAIN2}"
    
    cat <<EOF | oc --kubeconfig="$KUBECONFIG2" apply -f -
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
    - trustDomain: $APP_DOMAIN3
      bundleEndpointUrl: https://federation.$APP_DOMAIN3
      bundleEndpointProfile: https_web
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
    
    print_info "Cluster 2 resources deployed successfully"
}

deploy_spire_cluster3() {
    print_info "Deploying SPIRE resources on Cluster 3 ($CLUSTER3_NAME) with ACME..."
    
    local JWT_ISSUER="oidc-discovery.${APP_DOMAIN3}"
    
    cat <<EOF | oc --kubeconfig="$KUBECONFIG3" apply -f -
apiVersion: operator.openshift.io/v1alpha1
kind: ZeroTrustWorkloadIdentityManager
metadata:
  name: cluster
spec:
  trustDomain: $APP_DOMAIN3
  clusterName: $CLUSTER3_NAME
---
apiVersion: operator.openshift.io/v1alpha1
kind: SpireServer
metadata:
  name: cluster
spec:
  caSubject:
    commonName: $APP_DOMAIN3
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
      profile: https_web
      httpsWeb:
        acme:
          directoryUrl: "https://acme-v02.api.letsencrypt.org/directory"
          domainName: "federation.$APP_DOMAIN3"
          email: "$ACME_EMAIL"
          tosAccepted: "true"
    managedRoute: "true"
    federatesWith:
    - trustDomain: $APP_DOMAIN1
      bundleEndpointUrl: https://federation.$APP_DOMAIN1
      bundleEndpointProfile: https_spiffe
      endpointSpiffeId: spiffe://$APP_DOMAIN1/spire/server
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
    
    print_info "Cluster 3 resources deployed successfully"
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
    print_error "Troubleshooting:"
    print_error "  1. Check if the route exists: oc get route spire-server-federation -n zero-trust-workload-identity-manager"
    print_error "  2. Verify DNS resolution: nslookup federation.$cluster_name"
    print_error "  3. Try manual curl: curl -kv $bundle_endpoint_url"
    return 1
}

# Function to extract bundle from federation endpoint using curl
extract_bundle() {
    local bundle_endpoint_url=$1
    local cluster_name=$2
    local output_file=$3
    
    print_info "Fetching bundle from $cluster_name federation endpoint: $bundle_endpoint_url..."
    
    # Fetch the bundle using curl (insecure mode for self-signed certs)
    # Note: For https_web with ACME, the cert should be valid after ACME completes
    # For https_spiffe, it uses self-signed certs
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
    local profile_type=$5
    local endpoint_spiffe_id=$6
    local bundle_file=$7
    local federation_name=$8
    
    print_info "Creating federation $federation_name on $cluster_name..."
    
    local bundle_content
    bundle_content=$(cat "$bundle_file" | sed 's/^/    /')
    
    if [[ "$profile_type" == "https_spiffe" ]]; then
        cat <<EOF | oc --kubeconfig="$kubeconfig" apply -f -
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
    else
        cat <<EOF | oc --kubeconfig="$kubeconfig" apply -f -
apiVersion: spire.spiffe.io/v1alpha1
kind: ClusterFederatedTrustDomain
metadata:
  name: $federation_name
spec:
  trustDomain: $trust_domain
  bundleEndpointURL: $bundle_endpoint
  bundleEndpointProfile:
    type: https_web
  className: zero-trust-workload-identity-manager-spire
  trustDomainBundle: |-
$bundle_content
EOF
    fi
    
    print_info "Federation $federation_name created successfully"
}

# Main execution
print_info "=========================================="
print_info "3-Cluster Federation Setup"
print_info "=========================================="
print_info ""

# Step 1: Deploy SPIRE resources on all clusters
print_info "Step 1: Deploying SPIRE resources on all clusters..."
deploy_spire_cluster1
deploy_spire_cluster2
deploy_spire_cluster3
print_info ""

# Step 2: Wait for SPIRE servers to be ready
print_info "Step 2: Waiting for SPIRE servers to be ready..."
wait_for_spire_server "$KUBECONFIG1" "$CLUSTER1_NAME"
wait_for_spire_server "$KUBECONFIG2" "$CLUSTER2_NAME"
wait_for_spire_server "$KUBECONFIG3" "$CLUSTER3_NAME"
print_info ""

# Step 3: Wait for federation endpoints to be accessible
print_info "Step 3: Waiting for federation endpoints to be accessible..."
FEDERATION_URL1="https://federation.$APP_DOMAIN1"
FEDERATION_URL2="https://federation.$APP_DOMAIN2"
FEDERATION_URL3="https://federation.$APP_DOMAIN3"

wait_for_federation_endpoint "$FEDERATION_URL1" "$CLUSTER1_NAME"
wait_for_federation_endpoint "$FEDERATION_URL2" "$CLUSTER2_NAME"
wait_for_federation_endpoint "$FEDERATION_URL3" "$CLUSTER3_NAME"
print_info ""

# Step 4: Extract bundles from federation endpoints
print_info "Step 4: Fetching trust bundles from federation endpoints..."
BUNDLE1="${TEMP_DIR}/cluster1-bundle.json"
BUNDLE2="${TEMP_DIR}/cluster2-bundle.json"
BUNDLE3="${TEMP_DIR}/cluster3-bundle.json"

extract_bundle "$FEDERATION_URL1" "$CLUSTER1_NAME" "$BUNDLE1"
extract_bundle "$FEDERATION_URL2" "$CLUSTER2_NAME" "$BUNDLE2"
extract_bundle "$FEDERATION_URL3" "$CLUSTER3_NAME" "$BUNDLE3"
print_info ""

# Step 5: Create federation relationships
print_info "Step 5: Creating federation relationships..."

# Cluster 1 federations (to 2 and 3)
print_info "Creating federations on Cluster 1..."
create_federation "$KUBECONFIG1" "$CLUSTER1_NAME" "$APP_DOMAIN2" \
    "https://federation.$APP_DOMAIN2" \
    "https_spiffe" "spiffe://$APP_DOMAIN2/spire/server" "$BUNDLE2" \
    "cluster-12-federation"

create_federation "$KUBECONFIG1" "$CLUSTER1_NAME" "$APP_DOMAIN3" \
    "https://federation.$APP_DOMAIN3" \
    "https_web" "" "$BUNDLE3" \
    "cluster-13-federation"

# Cluster 2 federations (to 1 and 3)
print_info "Creating federations on Cluster 2..."
create_federation "$KUBECONFIG2" "$CLUSTER2_NAME" "$APP_DOMAIN1" \
    "https://federation.$APP_DOMAIN1" \
    "https_spiffe" "spiffe://$APP_DOMAIN1/spire/server" "$BUNDLE1" \
    "cluster-21-federation"

create_federation "$KUBECONFIG2" "$CLUSTER2_NAME" "$APP_DOMAIN3" \
    "https://federation.$APP_DOMAIN3" \
    "https_web" "" "$BUNDLE3" \
    "cluster-23-federation"

# Cluster 3 federations (to 1 and 2)
print_info "Creating federations on Cluster 3..."
create_federation "$KUBECONFIG3" "$CLUSTER3_NAME" "$APP_DOMAIN1" \
    "https://federation.$APP_DOMAIN1" \
    "https_spiffe" "spiffe://$APP_DOMAIN1/spire/server" "$BUNDLE1" \
    "cluster-31-federation"

create_federation "$KUBECONFIG3" "$CLUSTER3_NAME" "$APP_DOMAIN2" \
    "https://federation.$APP_DOMAIN2" \
    "https_spiffe" "spiffe://$APP_DOMAIN2/spire/server" "$BUNDLE2" \
    "cluster-32-federation"

print_info ""
print_info "=========================================="
print_info "Federation setup completed successfully!"
print_info "=========================================="
print_info ""
print_info "Summary:"
print_info "  Cluster 1 ($CLUSTER1_NAME): $APP_DOMAIN1 (https_spiffe)"
print_info "  Cluster 2 ($CLUSTER2_NAME): $APP_DOMAIN2 (https_spiffe)"
print_info "  Cluster 3 ($CLUSTER3_NAME): $APP_DOMAIN3 (https_web with ACME)"
print_info ""
print_info "Federation endpoints:"
print_info "  - Cluster 1: https://federation.$APP_DOMAIN1"
print_info "  - Cluster 2: https://federation.$APP_DOMAIN2"
print_info "  - Cluster 3: https://federation.$APP_DOMAIN3"
print_info ""
print_info "Federation relationships established:"
print_info "  - Cluster 1 ↔ Cluster 2"
print_info "  - Cluster 1 ↔ Cluster 3"
print_info "  - Cluster 2 ↔ Cluster 3"
print_info ""
print_info "Note: Trust bundles were fetched from federation endpoints using curl."
print_info "      Customers can manually fetch bundles anytime using:"
print_info "        curl -k https://federation.<trust-domain>"
print_info ""
print_info "Example: To manually fetch and update a bundle:"
print_info "  # Fetch bundle from remote cluster"
print_info "  curl -k https://federation.$APP_DOMAIN2 > bundle.json"
print_info "  # Update ClusterFederatedTrustDomain with the new bundle"
print_info "  oc edit clusterfederatedtrustdomain <name>"
print_info ""
print_info "Verify federation status:"
print_info "  oc --kubeconfig=$KUBECONFIG1 get clusterfederatedtrustdomains"
print_info "  oc --kubeconfig=$KUBECONFIG2 get clusterfederatedtrustdomains"
print_info "  oc --kubeconfig=$KUBECONFIG3 get clusterfederatedtrustdomains"