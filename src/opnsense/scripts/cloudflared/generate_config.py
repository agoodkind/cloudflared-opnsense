#!/usr/local/bin/python3

"""
Cloudflared configuration generator for OPNsense
Generates cloudflared config.yml from OPNsense settings
"""

import sys
import os
import json
import yaml
import xml.etree.ElementTree as ET

def get_opnsense_config():
    """Read cloudflared settings from OPNsense config.xml"""
    tree = ET.parse('/conf/config.xml')
    root = tree.getroot()
    cf = root.find('.//OPNsense/cloudflared')
    if cf is None:
        return None

    general = cf.find('general')
    tunnels = cf.find('tunnels')

    return {
        'enabled': general.findtext('enabled', '0') == '1',
        'token': general.findtext('token', ''),
        'tunnel_name': general.findtext('tunnel_name', ''),
        'tunnels': [
            {
                'hostname': t.findtext('hostname'),
                'service': t.findtext('service'),
                'url': t.findtext('url')
            }
            for t in (tunnels.findall('tunnel') if tunnels else [])
        ]
    }

def get_config():
    """Get configuration from OPNsense config"""
    cf_config = get_opnsense_config()
    if cf_config is None or not cf_config['enabled']:
        # Return minimal disabled config
        return {
            'tunnel': 'disabled',
            'credentials-file': '/usr/local/etc/cloudflared/cert.pem',
            'ingress': [{'service': 'http_status:503'}]
        }

    # Build cloudflared config from OPNsense settings
    config = {
        'tunnel': cf_config['tunnel_name'] or 'opnsense-tunnel',
        'credentials-file': '/usr/local/etc/cloudflared/cert.pem',
        'ingress': []
    }

    # Add tunnel ingress rules
    for tunnel in cf_config['tunnels']:
        if tunnel['hostname'] and tunnel['url']:
            ingress_rule = {
                'hostname': tunnel['hostname'],
                'service': tunnel['url']
            }
            # Add originRequest settings for HTTP services
            if tunnel['service'] in ['http', 'https']:
                ingress_rule['originRequest'] = {
                    'noTLSVerify': tunnel['url'].startswith('http://')
                }
            config['ingress'].append(ingress_rule)

    # Add default catch-all
    config['ingress'].append({'service': 'http_status:404'})

    return config

def main():
    config = get_config()

    # Write config to stdout (OPNsense will capture this)
    print(yaml.dump(config, default_flow_style=False))

if __name__ == '__main__':
    main()