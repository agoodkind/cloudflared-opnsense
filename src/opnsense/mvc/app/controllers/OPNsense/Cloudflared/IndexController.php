<?php

namespace OPNsense\Cloudflared;

use OPNsense\Base\IndexController;

/**
 * Class IndexController
 * @package OPNsense\Cloudflared
 */
class IndexController extends IndexController
{
    /**
     * default cloudflared index page
     * @throws \Exception
     */
    public function indexAction()
    {
        $this->view->title = "Cloudflared";
        $this->view->pick('OPNsense/Cloudflared/index');
    }

    /**
     * cloudflared settings page
     * @throws \Exception
     */
    public function settingsAction()
    {
        $this->view->title = "Cloudflared Settings";
        $this->view->generalForm = $this->getForm("general");
        $this->view->tunnelForm = $this->getForm("dialogTunnel");
        $this->view->tunnels = $this->getModel()->tunnels->tunnel->iterateItems();
        $this->view->pick('OPNsense/Cloudflared/settings');
    }
}