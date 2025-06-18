#!/usr/bin/env python3
"""
Argo CD Cluster Secret to Kubeconfig Converter

This script reads Argo CD cluster secrets from a Kubernetes cluster and converts
them into kubectl-compatible configuration. All configurations are written to
the same kubeconfig file as separate contexts.

Usage:
    python3 clustersecret-to-kubeconf.py --context <source-context> [options]
"""

import argparse
import base64
import json
import os
import sys
import yaml
from pathlib import Path
from typing import Dict, List, Optional, Any

try:
    from kubernetes import client, config
    from kubernetes.client.rest import ApiException
except ImportError:
    print("Error: kubernetes package not found. Please install it with:")
    print("pip install kubernetes")
    sys.exit(1)


class ArgoCDClusterSecretConverter:
    """Converts Argo CD cluster secrets to kubectl kubeconfig format."""
    
    def __init__(self, source_context: str, kubeconfig_path: str = None, insecure: bool = False):
        """
        Initialize the converter.
        
        Args:
            source_context: The Kubernetes context to use for reading secrets
            kubeconfig_path: Path to the kubeconfig file to write to
            insecure: Whether to skip TLS verification for the source cluster
        """
        self.source_context = source_context
        self.kubeconfig_path = kubeconfig_path or os.path.expanduser("~/.kube/argocd-clusters")
        self.insecure = insecure
        self.k8s_client = None
        self.existing_config = {}
        
    def load_k8s_client(self) -> None:
        """Load the Kubernetes client with the specified context."""
        try:
            if self.insecure:
                # For insecure connections, we need to disable SSL verification at the urllib3 level
                import urllib3
                urllib3.disable_warnings(urllib3.exceptions.InsecureRequestWarning)
                
                # Load the kubeconfig first to get the host information
                if self.source_context:
                    config.load_kube_config(context=self.source_context)
                else:
                    config.load_kube_config()
                
                # Get the current configuration that was loaded
                current_config = client.Configuration.get_default_copy()
                
                # Create a new configuration with the same host but disabled SSL verification
                configuration = current_config
                configuration.verify_ssl = False
                # Do NOT set ssl_ca_cert, cert_file, or key_file to None, to preserve authentication
                self.k8s_client = client.CoreV1Api(client.ApiClient(configuration))
            else:
                # Use standard kubeconfig loading
                if self.source_context:
                    config.load_kube_config(context=self.source_context)
                else:
                    config.load_kube_config()
                self.k8s_client = client.CoreV1Api()
            
        except Exception as e:
            print(f"Error loading Kubernetes client: {e}")
            print("If you're connecting to a development cluster with self-signed certificates,")
            print("try using the --insecure flag:")
            print("  python3 clustersecret-to-kubeconf.py --insecure")
            print("Or set KUBECONFIG environment variable or use --context to specify a different context.")
            sys.exit(1)
    
    def load_existing_kubeconfig(self) -> None:
        """Load existing kubeconfig if it exists."""
        if os.path.exists(self.kubeconfig_path):
            try:
                with open(self.kubeconfig_path, 'r') as f:
                    self.existing_config = yaml.safe_load(f) or {}
            except Exception as e:
                print(f"Warning: Could not load existing kubeconfig: {e}")
                self.existing_config = {}
        else:
            self.existing_config = {
                'apiVersion': 'v1',
                'kind': 'Config',
                'clusters': [],
                'users': [],
                'contexts': [],
                'current-context': ''
            }
    
    def get_argo_cd_cluster_secrets(self) -> List[Dict[str, Any]]:
        """
        Retrieve Argo CD cluster secrets from the Kubernetes cluster.
        
        Returns:
            List of cluster secret objects
        """
        try:
            # Look for secrets with Argo CD cluster labels
            # Argo CD typically uses these labels for cluster secrets
            label_selector = "argocd.argoproj.io/secret-type=cluster"
            
            secrets = self.k8s_client.list_secret_for_all_namespaces(
                label_selector=label_selector
            )
            
            if not secrets.items:
                print("No Argo CD cluster secrets found.")
                print("Make sure you're connected to the cluster that contains Argo CD cluster secrets.")
                print("Argo CD cluster secrets should have the label: argocd.argoproj.io/secret-type=cluster")
                return []
            
            return secrets.items
            
        except ApiException as e:
            print(f"Error retrieving secrets: {e}")
            return []
        except Exception as e:
            print(f"Unexpected error: {e}")
            return []
    
    def parse_cluster_secret(self, secret) -> Optional[Dict[str, Any]]:
        """
        Parse an Argo CD cluster secret and extract cluster configuration.
        
        Args:
            secret: Kubernetes secret object
            
        Returns:
            Dictionary containing cluster configuration or None if invalid
        """
        try:
            # Get the cluster configuration from the secret
            config_data = secret.data.get('config')
            if not config_data:
                print(f"Warning: No config data found in secret {secret.metadata.name}")
                return None
            
            # Decode the base64 encoded config
            config_str = base64.b64decode(config_data).decode('utf-8')
            cluster_config = json.loads(config_str)
            
            # Extract cluster information
            cluster_name = secret.metadata.name.replace('cluster-', '').replace('-cluster', '')
            
            # Check for server URL in multiple locations
            server_url = None
            
            # 1. Check in tlsClientConfig.server
            server_url = cluster_config.get('tlsClientConfig', {}).get('server', '')
            
            # 2. Check at top level of config
            if not server_url:
                server_url = cluster_config.get('server', '')
            
            # 3. Check directly in secret data (Argo CD format)
            if not server_url and 'server' in secret.data:
                server_url = base64.b64decode(secret.data['server']).decode('utf-8')
            
            if not server_url:
                print(f"Warning: No server URL found in secret {secret.metadata.name}")
                return None
            
            # Extract TLS configuration
            tls_config = cluster_config.get('tlsClientConfig', {})
            ca_data = tls_config.get('caData', '')
            cert_data = tls_config.get('certData', '')
            key_data = tls_config.get('keyData', '')
            
            # Extract basic auth if present
            basic_auth = cluster_config.get('username', ''), cluster_config.get('password', '')
            
            return {
                'name': cluster_name,
                'server': server_url,
                'ca_data': ca_data,
                'cert_data': cert_data,
                'key_data': key_data,
                'username': basic_auth[0],
                'password': basic_auth[1],
                'insecure_skip_tls_verify': tls_config.get('insecure', False)
            }
            
        except Exception as e:
            print(f"Error parsing secret {secret.metadata.name}: {e}")
            return None
    
    def add_cluster_to_kubeconfig(self, cluster_info: Dict[str, Any]) -> None:
        """
        Add a cluster configuration to the kubeconfig.
        
        Args:
            cluster_info: Dictionary containing cluster configuration
        """
        cluster_name = cluster_info['name']
        
        # Create cluster entry
        cluster_entry = {
            'name': cluster_name,
            'cluster': {
                'server': cluster_info['server'],
                'insecure-skip-tls-verify': cluster_info['insecure_skip_tls_verify']
            }
        }
        
        # Add CA certificate if present
        if cluster_info['ca_data']:
            cluster_entry['cluster']['certificate-authority-data'] = cluster_info['ca_data']
        
        # Create user entry
        user_entry = {
            'name': f"{cluster_name}-user",
            'user': {}
        }
        
        # Add client certificate if present
        if cluster_info['cert_data'] and cluster_info['key_data']:
            user_entry['user']['client-certificate-data'] = cluster_info['cert_data']
            user_entry['user']['client-key-data'] = cluster_info['key_data']
        
        # Add basic auth if present
        elif cluster_info['username'] and cluster_info['password']:
            user_entry['user']['username'] = cluster_info['username']
            user_entry['user']['password'] = cluster_info['password']
        
        # Create context entry
        context_entry = {
            'name': cluster_name,
            'context': {
                'cluster': cluster_name,
                'user': f"{cluster_name}-user"
            }
        }
        
        # Add to existing config
        self.existing_config['clusters'].append(cluster_entry)
        self.existing_config['users'].append(user_entry)
        self.existing_config['contexts'].append(context_entry)
        
        print(f"Added cluster: {cluster_name} -> {cluster_info['server']}")
    
    def write_kubeconfig(self) -> None:
        """Write the updated kubeconfig to file."""
        try:
            # Ensure the directory exists
            os.makedirs(os.path.dirname(self.kubeconfig_path), exist_ok=True)
            
            with open(self.kubeconfig_path, 'w') as f:
                yaml.dump(self.existing_config, f, default_flow_style=False)
            
            print(f"\nKubeconfig updated successfully: {self.kubeconfig_path}")
            print(f"Added {len(self.existing_config['contexts'])} new contexts")
            
        except Exception as e:
            print(f"Error writing kubeconfig: {e}")
            sys.exit(1)
    
    def convert(self) -> None:
        """Main conversion method."""
        context_display = self.source_context or "current context"
        print(f"Reading Argo CD cluster secrets from context: {context_display}")
        
        # Load Kubernetes client
        self.load_k8s_client()
        
        # Load existing kubeconfig
        self.load_existing_kubeconfig()
        
        # Get Argo CD cluster secrets
        secrets = self.get_argo_cd_cluster_secrets()
        
        if not secrets:
            print("No Argo CD cluster secrets found. Exiting.")
            return
        
        print(f"Found {len(secrets)} Argo CD cluster secrets")
        
        # Process each secret
        converted_count = 0
        for secret in secrets:
            cluster_info = self.parse_cluster_secret(secret)
            if cluster_info:
                self.add_cluster_to_kubeconfig(cluster_info)
                converted_count += 1
        
        if converted_count > 0:
            # Write updated kubeconfig
            self.write_kubeconfig()
            print(f"\nSuccessfully converted {converted_count} cluster secrets to kubeconfig contexts")
        else:
            print("No valid cluster secrets were converted.")


def main():
    """Main function to handle command line arguments and run the converter."""
    parser = argparse.ArgumentParser(
        description="Convert Argo CD cluster secrets to kubectl kubeconfig format",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Examples:
  # Convert using current context
  python3 clustersecret-to-kubeconf.py
  
  # Convert using specific context
  python3 clustersecret-to-kubeconf.py --context my-cluster
  
  # Convert and write to custom kubeconfig file
  python3 clustersecret-to-kubeconf.py --context my-cluster --kubeconfig /path/to/config
  
  # Convert from development cluster with self-signed certificates
  python3 clustersecret-to-kubeconf.py --insecure
  
  # Write to default location (~/.kube/argocd-clusters) using current context
  python3 clustersecret-to-kubeconf.py
  
  # List available contexts first
  kubectl config get-contexts
        """
    )
    
    parser.add_argument(
        '--context',
        required=False,
        help='Kubernetes context to use for reading Argo CD cluster secrets (default: current context)'
    )
    
    parser.add_argument(
        '--kubeconfig',
        help='Path to kubeconfig file to write to (default: ~/.kube/argocd-clusters)'
    )
    
    parser.add_argument(
        '--dry-run',
        action='store_true',
        help='Show what would be converted without writing to kubeconfig'
    )
    
    parser.add_argument(
        '--insecure',
        action='store_true',
        help='Skip TLS verification for the source cluster (useful for development clusters with self-signed certificates)'
    )
    
    args = parser.parse_args()
    
    # Create converter and run
    converter = ArgoCDClusterSecretConverter(
        source_context=args.context,
        kubeconfig_path=args.kubeconfig,
        insecure=args.insecure
    )
    
    if args.dry_run:
        print("DRY RUN MODE - No changes will be made")
        context_display = args.context or "current context"
        print(f"Would read Argo CD cluster secrets from context: {context_display}")
        # For dry run, we'll just show what secrets would be processed
        converter.load_k8s_client()
        secrets = converter.get_argo_cd_cluster_secrets()
        print(f"Would process {len(secrets)} Argo CD cluster secrets:")
        for secret in secrets:
            print(f"  - {secret.metadata.name}")
    else:
        converter.convert()


if __name__ == "__main__":
    main() 