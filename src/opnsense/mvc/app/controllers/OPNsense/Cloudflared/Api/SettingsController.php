<?php

namespace OPNsense\Cloudflared\Api;

use OPNsense\Base\ApiMutableModelControllerBase;
use OPNsense\Base\UserException;
use OPNsense\Core\Config;

/**
 * Class SettingsController
 * @package OPNsense\Cloudflared\Api
 */
class SettingsController extends ApiMutableModelControllerBase
{
    protected static $internalModelClass = '\OPNsense\Cloudflared\Settings';
    protected static $internalModelName = 'settings';

    /**
     * Get a single tunnel ingress rule
     * @param string $uuid item unique id
     * @return array tunnel data
     */
    public function getTunnelAction($uuid = null)
    {
        return $this->getBase('tunnel', 'tunnels.tunnel', $uuid);
    }

    /**
     * Add a new tunnel ingress rule
     * @return array save result
     */
    public function addTunnelAction()
    {
        return $this->addBase('tunnel', 'tunnels.tunnel');
    }

    /**
     * Update a tunnel ingress rule
     * @param string $uuid item unique id
     * @return array save result
     */
    public function setTunnelAction($uuid = null)
    {
        return $this->setBase('tunnel', 'tunnels.tunnel', $uuid);
    }

    /**
     * Delete a tunnel ingress rule
     * @param string $uuid item unique id
     * @return array delete result
     */
    public function delTunnelAction($uuid)
    {
        return $this->delBase('tunnels.tunnel', $uuid);
    }

    /**
     * Search tunnel ingress rules
     * @return array search results
     */
    public function searchTunnelAction()
    {
        return $this->searchBase('tunnels.tunnel', ['hostname', 'service', 'url', 'enabled']);
    }
}
