#!/usr/bin/env python3
"""
Test script for the Argo CD cluster secret converter.

This script creates mock Argo CD cluster secrets to test the converter functionality.
"""

import base64
import json
import yaml
from unittest.mock import Mock, patch
import sys
import os

# Add the current directory to the path so we can import our module
sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

# Import the main script as a module
import importlib.util
spec = importlib.util.spec_from_file_location("clustersecret_to_kubeconf", "clustersecret-to-kubeconf.py")
clustersecret_to_kubeconf = importlib.util.module_from_spec(spec)
spec.loader.exec_module(clustersecret_to_kubeconf)

ArgoCDClusterSecretConverter = clustersecret_to_kubeconf.ArgoCDClusterSecretConverter


def create_mock_secret(name, server_url, ca_data=None, cert_data=None, key_data=None, 
                      username=None, password=None, insecure=False):
    """Create a mock Argo CD cluster secret."""
    # Create the cluster configuration
    cluster_config = {
        'tlsClientConfig': {
            'server': server_url,
            'insecure': insecure
        }
    }
    
    if ca_data:
        cluster_config['tlsClientConfig']['caData'] = ca_data
    if cert_data:
        cluster_config['tlsClientConfig']['certData'] = cert_data
    if key_data:
        cluster_config['tlsClientConfig']['keyData'] = key_data
    if username:
        cluster_config['username'] = username
    if password:
        cluster_config['password'] = password
    
    # Create the secret object
    secret = Mock()
    secret.metadata.name = name
    secret.data = {
        'config': base64.b64encode(json.dumps(cluster_config).encode('utf-8'))
    }
    
    return secret


def test_converter():
    """Test the converter with mock data."""
    print("Testing Argo CD Cluster Secret Converter...")
    
    # Create mock secrets
    mock_secrets = [
        create_mock_secret(
            name='cluster-prod-cluster',
            server_url='https://prod-cluster.example.com:6443',
            ca_data='LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCg==',  # Mock CA data
            cert_data='LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCg==',  # Mock cert data
            key_data='LS0tLS1CRUdJTiBQUklWQVRFIEtFWS0tLS0tCg=='   # Mock key data
        ),
        create_mock_secret(
            name='cluster-staging-cluster',
            server_url='https://staging-cluster.example.com:6443',
            username='admin',
            password='secret-password',
            insecure=True
        ),
        create_mock_secret(
            name='cluster-dev-cluster',
            server_url='https://dev-cluster.example.com:6443',
            ca_data='LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCg==',
            cert_data='LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCg==',
            key_data='LS0tLS1CRUdJTiBQUklWQVRFIEtFWS0tLS0tCg=='
        )
    ]
    
    # Create converter instance
    converter = ArgoCDClusterSecretConverter('test-context')
    
    # Test parsing each secret
    print("\nTesting secret parsing:")
    for secret in mock_secrets:
        cluster_info = converter.parse_cluster_secret(secret)
        if cluster_info:
            print(f"✓ Successfully parsed {secret.metadata.name}")
            print(f"  Server: {cluster_info['server']}")
            print(f"  Name: {cluster_info['name']}")
            print(f"  Insecure: {cluster_info['insecure_skip_tls_verify']}")
        else:
            print(f"✗ Failed to parse {secret.metadata.name}")
    
    # Test kubeconfig generation
    print("\nTesting kubeconfig generation:")
    converter.load_existing_kubeconfig()
    
    for secret in mock_secrets:
        cluster_info = converter.parse_cluster_secret(secret)
        if cluster_info:
            converter.add_cluster_to_kubeconfig(cluster_info)
    
    # Print the generated kubeconfig
    print("\nGenerated kubeconfig structure:")
    print(f"Clusters: {len(converter.existing_config['clusters'])}")
    print(f"Users: {len(converter.existing_config['users'])}")
    print(f"Contexts: {len(converter.existing_config['contexts'])}")
    
    # Show the contexts
    for context in converter.existing_config['contexts']:
        print(f"  - {context['name']}")
    
    print("\nTest completed successfully!")


if __name__ == "__main__":
    test_converter() 