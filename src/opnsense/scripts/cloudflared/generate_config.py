#!/usr/local/bin/python3

"""
Cloudflared configuration generator for OPNsense

Outputs:
  --json   : JSON with all settings (for reconfigure.sh)
  --config : YAML config file (for config mode)
  (default): YAML config file
"""

import sys
import json
import xml.etree.ElementTree as ET
from typing import Any

# yaml may not be installed, handle gracefully
try:
    import yaml
    HAS_YAML = True
except ImportError:
    HAS_YAML = False


def get_opnsense_config() -> dict[str, Any] | None:
    """Read cloudflared settings from OPNsense config.xml"""
    try:
        tree = ET.parse('/conf/config.xml')
        root = tree.getroot()
    except Exception:
        return None

    cf = root.find('.//OPNsense/cloudflared')
    if cf is None:
        return None

    general = cf.find('general')
    if general is None:
        return None

    tunnels_elem = cf.find('tunnels')

    tunnels = []
    if tunnels_elem is not None:
        for t in tunnels_elem.findall('tunnel'):
            enabled = t.findtext('enabled', '1')
            if enabled == '1':
                tunnels.append({
                    'hostname': t.findtext('hostname', ''),
                    'service': t.findtext('service', 'http'),
                    'url': t.findtext('url', '')
                })

    return {
        'enabled': general.findtext('enabled', '0') == '1',
        'mode': general.findtext('mode', 'token'),
        'token': general.findtext('token', ''),
        'tunnel_name': general.findtext('tunnel_name', ''),
        'post_quantum': general.findtext('post_quantum', '1') == '1',
        'edge_ip_version': general.findtext('edge_ip_version', 'auto'),
        'protocol': general.findtext('protocol', 'auto'),
        'loglevel': general.findtext('loglevel', 'info'),
        'tunnels': tunnels
    }


def get_settings_json() -> dict[str, Any]:
    """Get all settings as JSON for reconfigure.sh"""
    cf_config = get_opnsense_config()
    if cf_config is None:
        return {
            'enabled': False,
            'mode': 'token',
            'token': '',
            'tunnel_name': '',
            'post_quantum': True,
            'edge_ip_version': 'auto',
            'protocol': 'auto',
            'loglevel': 'info',
            'tunnels': []
        }
    return cf_config


def get_config_yaml() -> dict[str, Any]:
    """Get configuration for config.yml (config mode only)"""
    cf_config = get_opnsense_config()
    if cf_config is None or not cf_config['enabled']:
        return {
            'tunnel': 'disabled',
            'credentials-file': '/usr/local/etc/cloudflared/cert.pem',
            'ingress': [{'service': 'http_status:503'}]
        }

    config: dict[str, Any] = {
        'tunnel': cf_config['tunnel_name'] or 'opnsense-tunnel',
        'credentials-file': '/usr/local/etc/cloudflared/cert.pem',
        'ingress': []
    }

    # Add tunnel ingress rules
    for tunnel in cf_config['tunnels']:
        if tunnel['hostname'] and tunnel['url']:
            ingress_rule: dict[str, Any] = {
                'hostname': tunnel['hostname'],
                'service': tunnel['url']
            }
            if tunnel['service'] in ['http', 'https']:
                ingress_rule['originRequest'] = {
                    'noTLSVerify': tunnel['url'].startswith('http://')
                }
            config['ingress'].append(ingress_rule)

    # Add default catch-all
    config['ingress'].append({'service': 'http_status:404'})

    return config


def output_yaml(config: dict[str, Any]) -> None:
    """Output config as YAML"""
    if HAS_YAML:
        print(yaml.dump(config, default_flow_style=False))
    else:
        # Fallback: simple YAML-like output
        def simple_yaml(obj: Any, indent: int = 0) -> str:
            lines = []
            prefix = '  ' * indent
            if isinstance(obj, dict):
                for k, v in obj.items():
                    if isinstance(v, (dict, list)):
                        lines.append(f"{prefix}{k}:")
                        lines.append(simple_yaml(v, indent + 1))
                    else:
                        lines.append(f"{prefix}{k}: {v}")
            elif isinstance(obj, list):
                for item in obj:
                    if isinstance(item, dict):
                        first = True
                        for k, v in item.items():
                            if first:
                                lines.append(f"{prefix}- {k}: {v}")
                                first = False
                            else:
                                lines.append(f"{prefix}  {k}: {v}")
                    else:
                        lines.append(f"{prefix}- {item}")
            return '\n'.join(lines)
        print(simple_yaml(config))


def main() -> None:
    output_format = 'config'
    if len(sys.argv) > 1:
        if sys.argv[1] == '--json':
            output_format = 'json'
        elif sys.argv[1] == '--config':
            output_format = 'config'

    if output_format == 'json':
        settings = get_settings_json()
        print(json.dumps(settings))
    else:
        config = get_config_yaml()
        output_yaml(config)


if __name__ == '__main__':
    main()