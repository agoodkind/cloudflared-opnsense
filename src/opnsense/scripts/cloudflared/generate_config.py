#!/usr/local/bin/python3

"""
Cloudflared configuration generator for OPNsense
Generates cloudflared config.yml from OPNsense settings
"""

import sys
import os
import json
import yaml

def get_config():
    """Get configuration from OPNsense config"""
    # This would normally read from OPNsense config system
    # For now, return a basic config
    config = {
        'tunnel': 'opnsense-tunnel',
        'credentials-file': '/usr/local/etc/cloudflared/cert.pem',
        'ingress': [
            {
                'hostname': '*.opnsense.local',
                'service': 'http://127.0.0.1',
                'originRequest': {
                    'noTLSVerify': True
                }
            },
            {
                'service': 'http_status:404'
            }
        ]
    }
    return config

def main():
    config = get_config()

    # Write config to stdout (OPNsense will capture this)
    print(yaml.dump(config, default_flow_style=False))

if __name__ == '__main__':
    main()