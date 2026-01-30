<?php

namespace OPNsense\Cloudflared;

use OPNsense\Base\IndexController as BaseIndexController;

/**
 * Class IndexController
 * @package OPNsense\Cloudflared
 */
class IndexController extends BaseIndexController
{
    /**
     * Default cloudflared status page
     * @throws \Exception
     */
    public function indexAction()
    {
        $this->view->title = gettext("Cloudflared Tunnel");
        $this->view->pick('OPNsense/Cloudflared/index');
    }

    /**
     * Cloudflared settings page
     * @throws \Exception
     */
    public function settingsAction()
    {
        $this->view->title = gettext("Cloudflared Settings");
        $this->view->generalForm = $this->getForm("general");
        $this->view->tunnelForm = $this->getForm("dialogTunnel");
        $this->view->tunnels = $this->getModel()->tunnels->tunnel->iterateItems();
        $this->view->pick('OPNsense/Cloudflared/settings');
    }
}
