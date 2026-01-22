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
}