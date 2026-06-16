<?php

namespace OPNsense\Cloudflared;

use OPNsense\Base\IndexController as BaseIndexController;

class SettingsController extends BaseIndexController
{
    public function indexAction()
    {
        $this->view->title = gettext("Cloudflared Settings");
        $this->view->generalForm = $this->getForm("general");
        $this->view->tunnelForm = $this->getForm("dialogTunnel");
        $this->view->pick('OPNsense/Cloudflared/settings');
    }
}
