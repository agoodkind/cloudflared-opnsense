#!/usr/local/bin/python3

"""
Check if cloudflared is enabled in OPNsense configuration
"""

import sys
import xml.etree.ElementTree as ET

def is_enabled():
    """Check if cloudflared is enabled in OPNsense config"""
    try:
        tree = ET.parse('/conf/config.xml')
        root = tree.getroot()
        cf = root.find('.//OPNsense/cloudflared')
        if cf is None:
            return False

        general = cf.find('general')
        if general is None:
            return False

        enabled = general.findtext('enabled', '0')
        return enabled == '1'
    except:
        return False

if __name__ == '__main__':
    sys.exit(0 if is_enabled() else 1)